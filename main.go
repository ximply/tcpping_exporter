package main

import (
	"flag"
	"net"
	"os"
	"net/http"
	"strings"
	"regexp"
	"github.com/ximply/tcpping_exporter/ping"
	"fmt"
	"github.com/robfig/cron"
	"io"
	"time"
	"strconv"
	"sync"
)

var (
	Name           = "tcpping_exporter"
	listenAddress  = flag.String("unix-sock", "/dev/shm/tcpping_exporter.sock", "Address to listen on for unix sock access and telemetry.")
	metricsPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	dest           = flag.String("dest", "", "Destination list to ping, multi split with ,(1.1.1.1:80,2.2.2.2:80).")
	count          = flag.Int("count", 1, "How many packages to ping.")
)

var destList []ping.Target
var g_lock sync.RWMutex
var g_result string
var doing bool

func isIP4(ip string) bool {
	b, _ := regexp.MatchString(`((25[0-5]|2[0-4]\d|((1\d{2})|([1-9]?\d)))\.){3}(25[0-5]|2[0-4]\d|((1\d{2})|([1-9]?\d)))`, ip)
	return b
}

func isDomain(domain string) bool {
	b, _ := regexp.MatchString(`[a-zA-Z0-9][-a-zA-Z0-9]{0,62}(.[a-zA-Z0-9][-a-zA-Z0-9]{0,62})+.?`, domain)
	return b
}

func doWork() {
	if doing {
		return
	}
	doing = true

	ret := ""
	namespace := "tcp_ping"
	for _, t := range destList {
		pinger := ping.NewTCPing()
		pinger.SetTarget(&t)
		pingerDone := pinger.Start()
		select {
		case <-pingerDone:
			break
		}
		ret += fmt.Sprintf("%s_delay{addr=\"%s\",port=\"%d\"} %g\n",
			namespace, t.Host, t.Port, float64(pinger.Result().MaxDuration) / float64(1000000))
	}

	g_lock.Lock()
	g_result = ret
	g_lock.Unlock()

	doing = false
}

func metrics(w http.ResponseWriter, r *http.Request) {
	g_lock.RLock()
	io.WriteString(w, g_result)
	g_lock.RUnlock()
}

func main() {
	flag.Parse()

	addr := "/dev/shm/tcpping_exporter.sock"
	if listenAddress != nil {
		addr = *listenAddress
	}

	if dest == nil || len(*dest) == 0 {
		panic("error dest")
	}
	l := strings.Split(*dest, ",")
	for _, i := range l {
		s := strings.Split(i, ":")
		if len(s) != 2 {
			continue
		}

		var t ping.Target
		t.Counter = *count
		t.Timeout = time.Second
		t.Host = s[0]
		t.Port, _ = strconv.Atoi(s[1])
		t.Interval = 20 * time.Millisecond
		t.Protocol = ping.TCP
		if isIP4(t.Host) || isDomain(t.Host) {
			destList = append(destList, t)
		}
	}

	if len(destList) == 0 {
		panic("no one to ping")
	}

	doing = false
	c := cron.New()
	c.AddFunc("0 */1 * * * ?", doWork)
	c.Start()

	mux := http.NewServeMux()
	mux.HandleFunc(*metricsPath, metrics)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Tcp Ping Exporter</title></head>
             <body>
             <h1>Tcp Ping Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	server := http.Server{
		Handler: mux, // http.DefaultServeMux,
	}
	os.Remove(addr)

	listener, err := net.Listen("unix", addr)
	if err != nil {
		panic(err)
	}
	server.Serve(listener)
}