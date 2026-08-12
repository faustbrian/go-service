package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	certificatePath := flag.String("certificate", "", "path for the generated trust certificate")
	readyPath := flag.String("ready", "", "path for the selected listener port")
	flag.Parse()
	if *certificatePath == "" || *readyPath == "" {
		fatal(fmt.Errorf("certificate and ready paths are required"))
	}
	certificate, err := generateCertificate()
	if err != nil {
		fatal(err)
	}
	defer func() { _ = os.RemoveAll(certificate.Directory) }()
	// #nosec G306 -- this is a public trust anchor mounted by an unprivileged container.
	if err := os.WriteFile(*certificatePath, certificate.CertificatePEM, 0o644); err != nil {
		fatal(err)
	}
	// #nosec G102 -- the disposable fixture must be reachable from its Docker network.
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "0.0.0.0:0")
	if err != nil {
		fatal(err)
	}
	server := &http.Server{
		ReadHeaderTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	if err := os.WriteFile(*readyPath, []byte(fmt.Sprintf("%d\n", listener.Addr().(*net.TCPAddr).Port)), 0o600); err != nil {
		_ = listener.Close()
		fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ServeTLS(listener, certificate.CertificatePath, certificate.PrivateKeyPath); err != nil &&
		err != http.ErrServerClosed {
		fatal(err)
	}
}

type generatedCertificate struct {
	CertificatePEM  []byte
	CertificatePath string
	Directory       string
	PrivateKeyPath  string
}

func generateCertificate() (generatedCertificate, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return generatedCertificate{}, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "reference-platform"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
		DNSNames:    []string{"host.docker.internal", "localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return generatedCertificate{}, err
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return generatedCertificate{}, err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	directory, err := os.MkdirTemp("", "reference-platform-fixture-*")
	if err != nil {
		return generatedCertificate{}, err
	}
	certificatePath := directory + "/server.crt"
	privateKeyPath := directory + "/server.key"
	if err := os.WriteFile(certificatePath, certificatePEM, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return generatedCertificate{}, err
	}
	if err := os.WriteFile(privateKeyPath, privatePEM, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return generatedCertificate{}, err
	}
	return generatedCertificate{
		CertificatePEM: certificatePEM, CertificatePath: certificatePath,
		Directory: directory, PrivateKeyPath: privateKeyPath,
	}, nil
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
