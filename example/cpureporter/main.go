package main

import (
	"flag"
	"fmt"
	"github.com/shirou/gopsutil/v3/cpu"
	"log"
	"net/http"
)

var (
	port = flag.Int("port", 6000, "-port 6000")
)

func main() {
	flag.Parse()
	http.HandleFunc("/cpu", CPUHandler)
	http.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("pong"))
	})

	log.Printf("cpureporter is listening on port :%d\n", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		log.Fatalln(err)
	}
}

func CPUHandler(w http.ResponseWriter, r *http.Request) {
	ps, err := cpu.PercentWithContext(r.Context(), 0, false)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, http.StatusText(http.StatusInternalServerError))
		return
	}

	var cpuPercent float64
	for _, v := range ps {
		cpuPercent += v
	}
	cpuPercent /= float64(len(ps))

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "%d", int(cpuPercent))
}
