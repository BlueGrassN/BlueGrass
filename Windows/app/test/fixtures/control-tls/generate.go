//go:build ignore

// Generate disposable loopback TLS material. Never install this CA in an OS
// trust store, package it with the application, or reuse these keys elsewhere.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func main() {
	output := flag.String("out", "", "fixture output directory")
	flag.Parse()
	if *output == "" {
		panic("-out is required")
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	now := time.Now().UTC()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "BlueGrass test-only CA"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(3650 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	check(err)
	// Apple applies a lifetime bound to TLS leaves even with a custom trust
	// anchor. Keep this fixture within one year and regenerate it in CI.
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.example.com"}, DNSNames: []string{"api.example.com"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(364 * 24 * time.Hour), BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	check(err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	check(err)
	check(os.MkdirAll(*output, 0700))
	for name, block := range map[string]*pem.Block{
		"ca.pem":         {Type: "CERTIFICATE", Bytes: caDER},
		"server.pem":     {Type: "CERTIFICATE", Bytes: leafDER},
		"server-key.pem": {Type: "PRIVATE KEY", Bytes: keyDER},
	} {
		check(os.WriteFile(filepath.Join(*output, name), pem.EncodeToMemory(block), 0600))
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
