package requester

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

type Worker struct {
	// Request is the request to be made.
	Request *http.Request

	// RequestBody is the body of the Request.
	RequestBody []byte

	// Client is the HTTP client to be made.
	Client *http.Client

	// Kp is the gain of proportional control.
	Kp int

	// SV is Set Value.
	SV uint

	// InitialMV is initial Manipulative Value.
	InitialMV int

	// Interval is the interval for observing the output (pv).
	Interval uint

	// BufferLength is the length of buffer. The observed value (pv) is the average value of the buffer.
	BufferLength uint

	// The reporter URL is the URL for observing the output. The response must be 0 to 100 int plain text.
	ReporterURL string

	// Macro is a placeholder to replace the control value (mv).
	Macro    string
	macroIdx int

	// Cmd is a external command to be executed.
	Cmd string

	// CmdArgs is the arguments of Cmd.
	CmdArgs []string

	Results chan State
}

// Init initializes internal data-structures
func (w *Worker) Init() error {
	w.Results = make(chan State)
	for idx, v := range w.CmdArgs {
		if v == w.Macro {
			w.macroIdx = idx
			return nil
		}
	}
	return fmt.Errorf("macro string not found")
}

// Validate validates the data-structures
func (w *Worker) Validate() error {
	if w.Request == nil {
		return fmt.Errorf("Request is nil")
	}
	if w.Client == nil {
		return fmt.Errorf("Client is nil")
	}
	if w.SV < 0 || 100 < w.SV {
		return fmt.Errorf("SV must be in 0 to 100")
	}
	return nil
}

// Run is a function that performs proportional control to the target system.
// It replaces mv with a macro and make the reporter URL response pv. dv is calculated by sv - pv.
func (w *Worker) Run(ctx context.Context) error {
	tick := time.Tick(time.Duration(w.Interval) * time.Millisecond)
	buffer := make([]uint, w.BufferLength)

	// mv is manipulative Variable
	mv := w.InitialMV

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			w.SetMacro(mv)
			cmd := exec.CommandContext(ctx, w.Cmd, w.CmdArgs...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			err := cmd.Start()
			if err != nil {
				return fmt.Errorf("failed to exec external command `%s`: %w", w.CmdArgs, err)
			}

			for i := 0; i < int(w.BufferLength); i++ {
				select {
				case <-ctx.Done():
					if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
						return fmt.Errorf("failed to kill external command: %w", err)
					}
					return nil

				default:
					<-tick
					req := cloneRequest(w.Request, w.RequestBody)
					resp, err := w.Client.Do(req)
					if err != nil {
						return fmt.Errorf("failed to send request: %w", err)
					}
					b, err := io.ReadAll(resp.Body)
					if err != nil {
						return fmt.Errorf("failed to read response body: %w", err)
					}
					resp.Body.Close()
					pv, err := strconv.ParseUint(string(b), 10, 64)
					if err != nil {
						return fmt.Errorf("failed to convert response body to uint: %w", err)
					}
					if pv < 0 || 100 < pv {
						return fmt.Errorf("process variable must be in 0 to 100: %w", err)
					}
					buffer[i] = uint(pv)
					w.Results <- State{MV: mv, PV: uint(pv)}
				}
			}

			if err := cmd.Process.Kill(); err != nil {
				//if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
				return fmt.Errorf("failed to kill external command: %w", err)
			}

			var sumPV uint
			for _, v := range buffer {
				sumPV += v
			}
			averagePV := uint(float64(sumPV) / float64(w.BufferLength))

			dv := int(w.SV) - int(averagePV)
			mv = mv + w.Kp * dv

		}
	}
}

// SetMacro replaces the macro with mv.
func (w *Worker) SetMacro(controlValue int) {
	w.CmdArgs[w.macroIdx] = strconv.Itoa(controlValue)
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

type State struct {
	MV int
	PV uint
}
