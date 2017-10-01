package main

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"log"
	"net"
	_ "net/http/pprof"

	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/mixer/wsplice"
	"gopkg.in/alecthomas/kingpin.v2"
)

var version = "master" // overwritten by goreleaser

var (
	host             = kingpin.Flag("listen", "Host and port to listen on.").Default("127.0.0.1:3000").String()
	network          = kingpin.Flag("network", "Network to listen on, should be either 'tcp' or 'tcp6' for IPv6 support").Default("tcp").String()
	allowedHostnames = kingpin.Flag("allowed-hostnames", "List of hostnames the server is allowed to connect dial out to.").Strings()
	pprofServer      = kingpin.Flag("pprof-address", "Address to host the pprof server on. This should not be exposed publicly. "+
		"If not provided, the pprof server will not be started").String()

	certFile = kingpin.Flag("tls-cert", "A PEM-encoded certificate file. Providing this enables TLS.").String()
	keyFile  = kingpin.Flag("tls-key", "A PEM encoded private key file. Providing this enables TLS.").String()
	caFile   = kingpin.Flag("tls-ca", "A PEM-encoded CA's cert. Providing this enabled client cert auth").String()

	frameSizeLimit = kingpin.Flag("frame-size-limit", "Maximum limit, in bytes, of control or close frames from the client").Default("5MB").Bytes()
	writeTimeout   = kingpin.Flag("write-timeout", "Write timeout for remote connections").Default("5s").Duration()
	readTimeout    = kingpin.Flag("read-timeout", "Read timeout for remote connections").Default("5s").Duration()
	dialTimeout    = kingpin.Flag("dial-timeout", "Dial timeout for creating remote connections").Default("10s").Duration()
)

func main() {
	kingpin.Version(version)
	kingpin.Parse()

	var (
		listener net.Listener
		err      error
	)

	if *certFile != "" {
		listener, err = tls.Listen(*network, *host, createTLSConfig())
	} else {
		listener, err = net.Listen(*network, *host)
	}

	if err != nil {
		logrus.WithError(err).Fatal("Error creating network listener")
	}

	go startPprof()

	config := &wsplice.Config{
		FrameSizeLimit:    int64(*frameSizeLimit),
		WriteTimeout:      *writeTimeout,
		ReadTimeout:       *readTimeout,
		DialTimeout:       *dialTimeout,
		HostnameAllowlist: *allowedHostnames,
	}

	logrus.Infof("wsplice listening on %s", *network)
	err = (&http.Server{Handler: &wsplice.Server{Config: config}}).Serve(listener)
	if err != nil {
		logrus.WithError(err).Fatal("Error listening for http connections")
	}
}

func createTLSConfig() *tls.Config {
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		logrus.WithError(err).Fatal("Error loading cert")
	}

	var certPool *x509.CertPool
	if *caFile != "" {
		caCert, err := ioutil.ReadFile(*caFile)
		if err != nil {
			log.Fatal(err)
		}
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(caCert)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      certPool,
	}
}

func startPprof() {
	if *pprofServer == "" {
		return
	}

	if err := http.ListenAndServe(*pprofServer, nil); err != nil {
		logrus.WithError(err).Warn("Error starting pprof server")
	}
}
