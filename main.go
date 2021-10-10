package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"github.com/tetsuzawa/heey/requester"
)

const (
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`
	heeyUA       = "heey/0.0.1"
)

var (
	kp           = flag.Int("kp", 1000, "proportional control gain")
	sv           = flag.Uint("sv", 50, "set variable")
	initialMV    = flag.Int("mv", 1000, "initial manipulative variable")
	interval     = flag.Uint("i", 1000, "interval [ms]")
	bufferLength = flag.Uint("l", 5, "buffer length")
	macro        = flag.String("macro", "%", "macro string")

	method      = flag.String("m", "GET", "")
	body        = flag.String("d", "", "")
	bodyFile    = flag.String("D", "", "")
	accept      = flag.String("A", "", "")
	contentType = flag.String("T", "text/plain", "")
	hs          headerSlice
	authHeader  = flag.String("a", "", "")
	hostHeader  = flag.String("host", "", "")
	userAgent   = flag.String("U", "", "")
	timeout     = flag.Int("t", 20, "")
	h2          = flag.Bool("h2", false, "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	disableRedirects   = flag.Bool("disable-redirects", false, "")
	proxyAddr          = flag.String("x", "", "")
)

var usage = `Usage: heey [options...] <reporter_url> "<external_command> <args>..."

Options:

  -kp      Proportional control gain. Default is 1000
  -sv      Set variable. heey performs proportional control so that pv becomes sv. 
           sv must be in 0 to 100. Default is 50.
  -mv      Initial manipulative variable. Default is 1000.
  -i       Interval of observation [ms]. Default is 1000
  -l       Buffer Length of observation. The observed value (pv) is the average value of the buffer.
  - macro  Macro is the placeholder to replace the control value (mv). Default is '%'.
  

  -m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -H  Custom HTTP header. You can specify as many as needed by repeating the flag.
      For example, -H "Accept: text/html" -H "Content-Type: application/xml" .
  -t  Timeout for each request in seconds. Default is 20, use 0 for infinite.
  -A  HTTP Accept header.
  -d  HTTP request body.
  -D  HTTP request body from file. For example, /home/user/file.txt or ./file.txt.
  -T  Content-type, defaults to "text/html".
  -U  User-Agent, defaults to version "hey/0.0.1".
  -a  Basic authentication, username:password.
  -x  HTTP Proxy address as host:port.
  -h2 Enable HTTP/2.

  -host	HTTP Host header.

  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -disable-redirects    Disable following of HTTP redirects
`

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))
	}

	flag.Var(&hs, "H", "")

	flag.Parse()
	if flag.NArg() < 2 {
		usageAndExit("")
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cancel()
	}()
	if err := run(ctx); err != nil {
		errAndExit(err.Error())
	}
}

func run(ctx context.Context) error {

	reporterUrl := flag.Arg(0)
	cmd := flag.Arg(1)

	httpMethod := strings.ToUpper(*method)

	// set content-type
	header := make(http.Header)
	header.Set("Content-Type", *contentType)

	// set any other additional repeatable headers
	for _, h := range hs {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			return fmt.Errorf("failed to parse header: %w", err)
		}
		header.Set(match[1], match[2])
	}

	if *accept != "" {
		header.Set("Accept", *accept)
	}

	// set basic auth if set
	var username, password string
	if *authHeader != "" {
		match, err := parseInputWithRegexp(*authHeader, authRegexp)
		if err != nil {
			return fmt.Errorf("failed to parse auth header: %w", err)
		}
		username, password = match[1], match[2]
	}

	var bodyAll []byte
	if *body != "" {
		bodyAll = []byte(*body)
	}
	if *bodyFile != "" {
		slurp, err := ioutil.ReadFile(*bodyFile)
		if err != nil {
			return fmt.Errorf("failed to read body file: %w", err)
		}
		bodyAll = slurp
	}

	var proxyURL *url.URL
	if *proxyAddr != "" {
		var err error
		proxyURL, err = url.Parse(*proxyAddr)
		if err != nil {
			return fmt.Errorf("proxy address is invalid: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, reporterUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.ContentLength = int64(len(bodyAll))
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	// set host header if set
	if *hostHeader != "" {
		req.Host = *hostHeader
	}

	ua := header.Get("User-Agent")
	if ua == "" {
		ua = heeyUA
	} else {
		ua += " " + heeyUA
	}
	header.Set("User-Agent", ua)

	// set userAgent header if set
	if *userAgent != "" {
		ua = *userAgent + " " + heeyUA
		header.Set("User-Agent", ua)
	}

	req.Header = header

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         req.Host,
		},
		DisableCompression: *disableCompression,
		DisableKeepAlives:  *disableKeepAlives,
		Proxy:              http.ProxyURL(proxyURL),
	}
	if *h2 {
		err := http2.ConfigureTransport(tr)
		if err != nil {
			return fmt.Errorf("failed to configure http2 transport: %w", err)
		}
	} else {
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	client := &http.Client{Transport: tr, Timeout: time.Duration(*timeout) * time.Second}
	if *disableRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	cmds := strings.Split(cmd, " ")

	w := &requester.Worker{
		Request:     req,
		RequestBody: bodyAll,
		Client:      client,

		Kp:           *kp,
		SV:           *sv,
		InitialMV:    *initialMV,
		Interval:     *interval,
		BufferLength: *bufferLength,
		Macro:        *macro,
		ReporterURL:  reporterUrl,

		Cmd:     cmds[0],
		CmdArgs: cmds[1:],
	}

	err = w.Init()
	if err != nil {
		return fmt.Errorf("failed to initialize worker: %w", err)
	}
	if err := w.Validate(); err != nil {
		return fmt.Errorf("worker validation error: %w", err)
	}

	err = w.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}
	return nil
}

func errAndExit(msg string) {
	fmt.Fprintf(os.Stderr, msg)
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg)
		fmt.Fprintf(os.Stderr, "\n\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

type headerSlice []string

func (h *headerSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *headerSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}
