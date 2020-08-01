# httpqotdd - HTTP Quote of the Day Daemon

This simple HTTP server serves up random quotes sourced
from a file or http(s) url. It can reload its list of
quotes on a timer or SIGHUP, and can cache a quote
selection for a period of time.

First, install the application with:
```
$ go get -u github.com/jktr/httpqotdd
```

You can then run the server like this:
```
$ httpqotdd -port 8080 -cache 1h -reload 24h ./example.txt
```

The input file format looks like this:
```
first string

# comment
second one is
a multiline string

third one has
\
a blank line

# also a comment

\#fourthstring
```

There is no TLS support; use a reverse proxy for that.
