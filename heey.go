package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

const (
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`
	heeyUA       = "heey/0.0.1"
)

// heey -k 10000 -i 1000 -l 5 http://target.com:6666/cpu_usage "hey -c 10 -n 100000 -q % 'http://example.com'"

var (
	m           = flag.String("m", "GET", "")
	body        = flag.String("d", "", "")
	bodyFile    = flag.String("D", "", "")
	accept      = flag.String("A", "", "")
	contentType = flag.String("T", "text/plain", "")
	authHeader  = flag.String("a", "", "")
	hostHeader  = flag.String("host", "", "")
	userAgent   = flag.String("U", "", "")
	timeout     = flag.Int("t", 20, "")

	gain         = flag.Int("k", 10000, "gain")
	setPoint     = flag.Uint("s", 50, "set point")
	interval     = flag.Uint("i", 1000, "interval [ms]")
	initialValue = flag.Int("v", 1000, "initial value")
	bufferLength = flag.Uint("l", 5, "buffer length")
	macro        = flag.String("macro", "%", "macro")

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

	var hs headerSlice
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

	reporterUrl := flag.Arg(0)
	cmd := flag.Arg(1)

	method := strings.ToUpper(*m)

	// set content-type
	header := make(http.Header)
	header.Set("Content-Type", *contentType)

	// set any other additional repeatable headers
	for _, h := range hs {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			usageAndExit(err.Error())
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
			usageAndExit(err.Error())
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
			errAndExit(err.Error())
		}
		bodyAll = slurp
	}

	var proxyURL *url.URL
	if *proxyAddr != "" {
		var err error
		proxyURL, err = url.Parse(*proxyAddr)
		if err != nil {
			usageAndExit(err.Error())
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, reporterUrl, nil)
	if err != nil {
		usageAndExit(err.Error())
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
		http2.ConfigureTransport(tr)
	} else {
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	client := &http.Client{Transport: tr, Timeout: time.Duration(*timeout) * time.Second}

	cmds := strings.Split(cmd, " ")

	// TODO validation
	w := &Work{
		Request:     req,
		RequestBody: bodyAll,
		Client:      client,

		Gain:         *gain,
		SetPoint:     *setPoint,
		InitialValue: *initialValue,
		Interval:     *interval,
		BufferLength: *bufferLength,
		ReporterURL:  reporterUrl,
		Macro:        *macro,
		Cmd:          cmds[0],
		CmdArgs:      cmds[1:],
	}

	err = w.Init()
	if err != nil {
		//TODO
	}
	err = w.Run(ctx)
	if err !=nil {
	}

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

type Work struct {
	// Request is the request to be made.
	Request *http.Request

	RequestBody []byte

	Client *http.Client

	results chan *result

	Gain         int
	SetPoint     uint
	Interval     uint
	InitialValue int
	BufferLength uint
	ReporterURL  string
	Macro        string
	MacroIdx     int
	Cmd          string
	CmdArgs      []string

	initOnce sync.Once
}

func (w *Work) GenHttpClient(ctx context.Context) (*http.Client, error) {
	// TODO custom client
	return http.DefaultClient, nil
}

func (w *Work) Run(ctx context.Context) error {
	throttle := time.Tick(time.Duration(w.Interval) * time.Microsecond)
	buffer := make([]uint, w.BufferLength)

	controlValue := w.InitialValue
	for {

		select {
		case <-ctx.Done():
			return nil
		default:
			w.SetMacro(controlValue)
			cmd := exec.CommandContext(ctx, w.Cmd, w.CmdArgs...)
			err := cmd.Start()
			if err != nil {
				// TODO
			}

			for i := 0; i < int(w.BufferLength); i++ {
				select {
				case <-ctx.Done():
					return nil
				default:
					req := cloneRequest(w.Request, w.RequestBody)
					<-throttle
					//w.makeRequest()
					resp, err := w.Client.Do(req)
					if err != nil {
						// TODO
					}
					b, err := io.ReadAll(resp.Body)
					if err != nil {
						// TODO
					}
					resp.Body.Close()
					observedValue, err := strconv.ParseUint(string(b), 10, 64)
					if err != nil {
						// TODO
					}
					if observedValue < 0 || 100 < observedValue {
						// TODO
					}
					buffer[i] = uint(observedValue)
				}
			}

			// TODO kill gracefully
			err = cmd.Process.Kill()
			if err != nil {
				// TODO
			}

			//
			var sum uint
			for _, v := range buffer {
				sum += v
			}
			average := uint(float64(sum) / float64(w.BufferLength))
			errorValue := int(w.SetPoint) - int(average)
			controlValue = w.Gain * errorValue
		}
	}
}

func (w *Work) makeRequest() {
	req := cloneRequest(w.Request, w.RequestBody)
	req = req.WithContext(req.Context())
	var size int64
	var code int
	resp, err := w.Client.Do(req)
	if err == nil {
		size = resp.ContentLength
		code = resp.StatusCode
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	w.results <- &result{
		statusCode:    code,
		err:           err,
		contentLength: size,
	}
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
func cloneRequest(r *http.Request, body []byte) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		r2.Header[k] = append([]string(nil), s...)
	}
	if len(body) > 0 {
		r2.Body = ioutil.NopCloser(bytes.NewReader(body))
	}
	return r2
}

type result struct {
	err           error
	statusCode    int
	contentLength int64
}


func findIdx(sl []string, target string) (int, error) {
	for idx, v := range sl {
		if v == target {
			return idx, nil
		}
	}
	// TODO
	return -1, fmt.Errorf("macro not found")
}

// Init initializes internal data-structures
func (w *Work) Init() error {
	var err error
	w.initOnce.Do(func() {
		w.MacroIdx, err = findIdx(w.CmdArgs, w.Macro)
	})
	return err
}

func (w *Work) SetMacro(controlValue int) {
	w.CmdArgs[w.MacroIdx] = strconv.Itoa(controlValue)
}
