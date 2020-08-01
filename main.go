// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	addr    string
	port    string
	reload  time.Duration
	cache   time.Duration
	verbose bool

	quote   *string
	quotes  *[]string
	quotesM sync.RWMutex
)

func init() {
	flag.Usage = func() {
		fmt.Printf("Usage: %s [OPTIONS] (FILE|URL)\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.StringVar(&port, "port", "8080", "bind to `port`")
	flag.StringVar(&addr, "addr", "[::1]", "bind to `address`")
	flag.DurationVar(&reload, "reload", 0, "quote source refresh `interval` (0 = no refresh; default 0)")
	flag.DurationVar(&cache, "cache", 0, "`duration` to cache selected quote (0 = don't cache; default 0)")
	flag.BoolVar(&verbose, "verbose", false, "verbose output: reloads / cache selections / access logs")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		log.Fatal("missing quote source")
	}
}

func handleQuote(w http.ResponseWriter, r *http.Request) {
	selection := selectQuote()
	if selection == nil {
		w.WriteHeader(503)
		return
	}
	fmt.Fprintln(w, *selection)

	if verbose {
		log.Printf(`%s "%s %s %s" "%s"`+"\n",
			r.RemoteAddr, r.Method, r.URL, r.Proto,
			r.Header.Get("User-Agent"))
	}
}

func loadQuotesFromFile(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	qs, err := parseQuotes(f)
	return qs, err
}

func loadQuotesFromURL(url string) ([]string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return []string{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return []string{}, errors.New("failed fetching quote source: " + strconv.Itoa(resp.StatusCode))
	}

	qs, err := parseQuotes(resp.Body)
	return qs, err
}

func fetchQuotes(source string) ([]string, error) {
	switch {
	case strings.HasPrefix(source, "https://"):
		return loadQuotesFromURL(source)
	case strings.HasPrefix(source, "http://"):
		return loadQuotesFromURL(source)
	default:
		return loadQuotesFromFile(source)
	}
}

func parseQuotes(r io.Reader) ([]string, error) {
	qs := []string{}
	acc := []string{}

	scan := bufio.NewScanner(r)
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "\\#") {
			line = line[1:]
		}

		if len(line) > 0 {
			if line == "\\" {
				line = ""
			}
			acc = append(acc, line)
		} else if len(acc) > 0 {
			qs = append(qs, strings.Join(acc, "\n"))
			acc = []string{}
		}
	}

	qs = append(qs, strings.Join(acc, "\n"))
	return qs, nil
}

func selectQuote() *string {
	quotesM.RLock()
	defer quotesM.RUnlock()

	if cache > 0 {
		return quote
	}

	return nextQuoteRaw()
}

func nextQuoteRaw() *string {
	if quotes == nil || len(*quotes) == 0 {
		return nil
	}

	idx := rand.Intn(len(*quotes))
	return &(*quotes)[idx]
}

func reloadQuotes(source string) error {
	newQuotes, err := fetchQuotes(source)
	if err != nil {
		return err
	}
	quotesM.Lock()
	quotes = &newQuotes
	quote = nextQuoteRaw()
	quotesM.Unlock()
	if verbose {
		log.Println("quotes reloaded; cached quote reselected")
	}
	return nil
}

func main() {

	source := os.Args[len(os.Args)-1]
	if err := reloadQuotes(source); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleQuote)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if quotes != nil || len(*quotes) == 0 {
			w.WriteHeader(503)
		}
	})

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan,
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGHUP)

	srv := http.Server{Addr: addr + ":" + port, Handler: mux}
	go func() {
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	go func() {
		if reload > 0 {
			t := time.NewTicker(reload)
			for {
				<-t.C
				if err := reloadQuotes(source); err != nil {
					log.Println(err)
				}
			}
		}
	}()

	go func() {
		if cache > 0 {
			t := time.NewTicker(cache)
			for {
				<-t.C
				quotesM.Lock()
				if quote = nextQuoteRaw(); quote != nil && verbose {
					log.Println("cached quote reselected")
				}
				quotesM.Unlock()
			}
		}
	}()

	for {
		select {
		case sig := <-sigchan:
			switch sig {
			case syscall.SIGHUP:
				log.Println("caught SIGHUP; reloading…")
				if err := reloadQuotes(source); err != nil {
					log.Println(err)
				}
			default:
				log.Println("caught signal; shutting down…")
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := srv.Shutdown(shutdown); err != nil {
					log.Fatal("server shutdown failed")
				}
				return
			}
		}
	}
}
