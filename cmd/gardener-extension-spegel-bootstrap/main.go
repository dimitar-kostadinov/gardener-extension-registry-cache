// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	kubeapiserverconstants "github.com/gardener/gardener/pkg/component/kubernetes/apiserver/constants"
	"github.com/gardener/gardener/pkg/logger"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	certutil "k8s.io/client-go/util/cert"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

const (
	// Token & CA files for shoot cluster access
	tokenFile = "/var/run/secrets/cluster-access/token"
	caFile    = "/var/run/secrets/cluster-access/ca.crt"

	// Server cert & key files
	tlsCertFile = "/var/run/secrets/certs/tls.crt"
	tlsKeyFile  = "/var/run/secrets/certs/tls.key"
	// CA bundle file used for client verification
	tlsCAFile = "/var/run/secrets/certs/bundle.crt"
)

var (
	ctx    context.Context
	log    logr.Logger
	client *kubernetes.Clientset
	limit  int = 10
)

// healthHandler handles only health check requests on HTTP
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	_, err := w.Write([]byte(`{"status": "ok"}`))
	if err != nil {
		log.Error(err, "Failed to write health response")
	}
}

func peerHandler(w http.ResponseWriter, _ *http.Request) {
	log := log.WithName("peerHandler")

	nodeList, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Error(err, "Fail to list nodes")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var ips []net.IPAddr
	for _, node := range nodeList.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				if ip, err := netip.ParseAddr(addr.Address); err == nil {
					ips = append(ips, net.IPAddr{IP: net.IP(ip.AsSlice()).To16(), Zone: ip.Zone()})
				}
				break
			}
		}
		//zone, _ = node.Labels["topology.kubernetes.io/zone"]
	}

	if len(ips) == 0 {
		err = fmt.Errorf("empty Node list")
		log.Error(err, "Node list is empty")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slices.SortFunc(ips, func(a, b net.IPAddr) int {
		return strings.Compare(a.String(), b.String())
	})
	log.Info("IPAddr", "ips", ips)
	if len(ips) > limit {
		ips = ips[:limit]
		log.Info("Limiting IPAddr", "ips", ips)
	}

	ipsBytes, err := json.Marshal(ips)
	if err != nil {
		log.Error(err, "Fail to marshal ips")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	_, err = w.Write(ipsBytes)
	if err != nil {
		log.Error(err, "Failed to write ips")
	}
}

func main() {
	logf.SetLogger(logger.MustNewZapLogger(logger.InfoLevel, logger.FormatJSON))
	ctx = signals.SetupSignalHandler()

	log = logf.Log.WithName("registry-spegel-peers")

	var err error

	client, err = getClient()
	if err != nil {
		log.Error(err, "APIServer client creation failed")
		os.Exit(1)
	}

	// Start health server in a goroutine
	http.HandleFunc("GET /health", healthHandler)
	go func() {
		log.Info("Starting HTTP health server", "port", 8080)
		if err := http.ListenAndServe(":8080", nil); err != nil && err != http.ErrServerClosed {
			log.Error(err, "Health server failed")
			os.Exit(1)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /bootstrap-nodes", peerHandler)

	// Main server for spegel peers endpoint
	srv := &http.Server{
		Handler:           mux,
		Addr:              ":443",
		WriteTimeout:      15 * time.Second,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 15 * time.Second,
	}

	tlsConfig, err := setupTLSConfig(tlsCertFile, tlsKeyFile, tlsCAFile)
	if err != nil {
		log.Error(err, "Fail to configure TLS")
		os.Exit(1)
	}
	srv.TLSConfig = tlsConfig

	log.Info("Starting HTTP spegel peers server", "port", 443)
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		log.Error(err, "Spegel peers server fails")
		os.Exit(1)
	}
}

func getClient() (*kubernetes.Clientset, error) {
	log.Info("Initializing client")

	if _, err := os.Stat(tokenFile); err != nil {
		return nil, err
	}
	if _, err := os.Stat(caFile); err != nil {
		return nil, err
	}

	tlsClientConfig := rest.TLSClientConfig{}

	if _, err := certutil.NewPool(caFile); err != nil {
		return nil, err
	} else {
		tlsClientConfig.CAFile = caFile
	}

	c := &rest.Config{
		Host:            "https://" + v1beta1constants.DeploymentNameKubeAPIServer + ":" + strconv.Itoa(kubeapiserverconstants.Port),
		TLSClientConfig: tlsClientConfig,
		BearerTokenFile: tokenFile,
	}

	client, err := kubernetes.NewForConfig(c)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// setupTLSConfig creates a TLS configuration with client certificate verification
func setupTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	// Load server certificate and key
	serverCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate and key: %w", err)
	}

	// Client certificate
	caCert, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate file: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	return tlsConfig, nil
}
