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

// heey -k 10000 -i 1000 -l 5 http://target.com:6666/cpu_usage "hey -c 10 -n 100000 -q % 'http://example.com'"

var (
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

	kp           = flag.Int("kp", 10000, "proportional control gain")
	sv           = flag.Uint("sv", 50, "set variable")
	initialMV    = flag.Int("mv", 1000, "initial manipulative variable")
	interval     = flag.Uint("i", 1000, "interval [ms]")
	bufferLength = flag.Uint("l", 5, "buffer length")
	macro        = flag.String("macro", "%", "macro string")

	h2 = flag.Bool("h2", false, "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	disableRedirects   = flag.Bool("disable-redirects", false, "")
	proxyAddr          = flag.String("x", "", "")
)

var usage = `TODO
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
		var err  error
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
