package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/coreos/pkg/capnslog"
	"github.com/coreos/pkg/flagutil"

	"github.com/openshift/console/auth"
	"github.com/openshift/console/pkg/proxy"
	"github.com/openshift/console/server"
)

var (
	log = capnslog.NewPackageLogger("github.com/openshift/console", "cmd/main")
)

const (
	k8sInClusterCA          = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	k8sInClusterBearerToken = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

func main() {
	rl := capnslog.MustRepoLogger("github.com/openshift/console")
	capnslog.SetFormatter(capnslog.NewStringFormatter(os.Stderr))

	fs := flag.NewFlagSet("bridge", flag.ExitOnError)
	fListen := fs.String("listen", "http://0.0.0.0:9000", "")

	fBaseAddress := fs.String("base-address", "", "Format: <http | https>://domainOrIPAddress[:port]. Example: https://tectonic.example.com.")
	fBasePath := fs.String("base-path", "/", "")

	fTectonicClusterName := fs.String("tectonic-cluster-name", "tectonic", "The Tectonic cluster name.")

	fUserAuth := fs.String("user-auth", "disabled", "disabled | oidc | openshift")
	fUserAuthOIDCIssuerURL := fs.String("user-auth-oidc-issuer-url", "", "The OIDC/OAuth2 issuer URL.")
	fUserAuthOIDCClientID := fs.String("user-auth-oidc-client-id", "", "The OIDC OAuth2 Client ID.")
	fUserAuthOIDCClientSecret := fs.String("user-auth-oidc-client-secret", "", "The OIDC OAuth2 Client Secret.")
	fUserAuthOIDCClientSecretFile := fs.String("user-auth-oidc-client-secret-file", "", "File containing the OIDC OAuth2 Client Secret.")

	fK8sMode := fs.String("k8s-mode", "in-cluster", "in-cluster | off-cluster")
	fK8sModeOffClusterEndpoint := fs.String("k8s-mode-off-cluster-endpoint", "", "URL of the Kubernetes API server.")
	fK8sModeOffClusterSkipVerifyTLS := fs.Bool("k8s-mode-off-cluster-skip-verify-tls", false, "DEV ONLY. When true, skip verification of certs presented by k8s API server.")

	fK8sAuth := fs.String("k8s-auth", "service-account", "service-account | bearer-token | oidc | openshift")
	fK8sAuthBearerToken := fs.String("k8s-auth-bearer-token", "", "Authorization token to send with proxied Kubernetes API requests.")

	fLogLevel := fs.String("log-level", "", "level of logging information by package (pkg=level).")
	fPublicDir := fs.String("public-dir", "./frontend/public/dist", "directory containing static web assets.")
	fTlSCertFile := fs.String("tls-cert-file", "", "TLS certificate. If the certificate is signed by a certificate authority, the certFile should be the concatenation of the server's certificate followed by the CA's certificate.")
	fTlSKeyFile := fs.String("tls-key-file", "", "The TLS certificate key.")
	fCAFile := fs.String("ca-file", "", "PEM File containing trusted certificates of trusted CAs. If not present, the system's Root CAs will be used. Not required for in-cluster clients to determine the expiration date for /tectonic/certs endpoint.")
	fTectonicVersion := fs.String("tectonic-version", "UNKNOWN", "The current tectonic system version, served at /version")
	fDexClientCertFile := fs.String("dex-client-cert-file", "", "PEM File containing certificates of dex client.")
	fDexClientKeyFile := fs.String("dex-client-key-file", "", "PEM File containing certificate key of the dex client.")
	fDexClientCAFile := fs.String("dex-client-ca-file", "", "PEM File containing trusted CAs for Dex client configuration. If blank, defaults to value of ca-file argument")

	fKubectlClientID := fs.String("kubectl-client-id", "", "The OAuth2 client_id of kubectl.")
	fKubectlClientSecret := fs.String("kubectl-client-secret", "", "The OAuth2 client_secret of kubectl.")
	fKubectlClientSecretFile := fs.String("kubectl-client-secret-file", "", "File containing the OAuth2 client_secret of kubectl.")
	fK8sPublicEndpoint := fs.String("k8s-public-endpoint", "", "Endpoint to use when rendering kubeconfigs for clients. Useful for when bridge uses an internal endpoint clients can't access for communicating with the API server.")

	fOpenshiftConsoleURL := fs.String("openshift-console-url", "", "URL for OpenShift console used in context switcher")

	fDexAPIHost := fs.String("dex-api-host", "", "Target host and port of the Dex API service.")
	fLogoImageName := fs.String("logo-image-name", "", "Logo image used in the masthead")
	fGoogleTagManagerID := fs.String("google-tag-manager-id", "", "Google Tag Manager ID. External analytics are disabled if this is not set.")

	fLoadTestFactor := fs.Int("load-test-factor", 0, "DEV ONLY. The factor used to multiply k8s API list responses for load testing purposes.")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if err := flagutil.SetFlagsFromEnv(fs, "BRIDGE"); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	baseURL := &url.URL{}
	if *fBaseAddress != "" {
		baseURL = validateFlagIsURL("base-address", *fBaseAddress)
	}

	if *fDexClientCAFile == "" {
		*fDexClientCAFile = *fCAFile
	}

	if !strings.HasPrefix(*fBasePath, "/") || !strings.HasSuffix(*fBasePath, "/") {
		flagFatalf("base-path", "value must start and end with slash")
	}
	baseURL.Path = *fBasePath

	caCertFilePath := *fCAFile
	if *fK8sMode == "in-cluster" {
		caCertFilePath = k8sInClusterCA
	}

	if *fOpenshiftConsoleURL != "" && !strings.HasSuffix(*fOpenshiftConsoleURL, "/") {
		flagFatalf("openshift-console-url", "value must end with slash")
	}

	srv := &server.Server{
		PublicDir:           *fPublicDir,
		TectonicVersion:     *fTectonicVersion,
		BaseURL:             baseURL,
		TectonicCACertFile:  caCertFilePath,
		ClusterName:         *fTectonicClusterName,
		OpenshiftConsoleURL: *fOpenshiftConsoleURL,
		LogoImageName:       *fLogoImageName,
		GoogleTagManagerID:  *fGoogleTagManagerID,
		LoadTestFactor:      *fLoadTestFactor,
	}

	if (*fKubectlClientID == "") != (*fKubectlClientSecret == "" && *fKubectlClientSecretFile == "") {
		fmt.Fprintln(os.Stderr, "Must provide both --kubectl-client-id and --kubectl-client-secret or --kubectrl-client-secret-file")
		os.Exit(1)
	}

	if *fKubectlClientSecret != "" && *fKubectlClientSecretFile != "" {
		fmt.Fprintln(os.Stderr, "Cannot provide both --kubectl-client-secret and --kubectrl-client-secret-file")
		os.Exit(1)
	}

	capnslog.SetGlobalLogLevel(capnslog.INFO)
	if *fLogLevel != "" {
		llc, err := rl.ParseLogLevelConfig(*fLogLevel)
		if err != nil {
			log.Fatal(err)
		}
		rl.SetLogLevel(llc)
		log.Infof("Setting log level to %s", *fLogLevel)
	}

	var (
		// Hold on to raw certificates so we can render them in kubeconfig files.
		dexCertPEM []byte
		k8sCertPEM []byte
	)
	if *fCAFile != "" {
		var err error

		if dexCertPEM, err = ioutil.ReadFile(*fCAFile); err != nil {
			log.Fatalf("Failed to read cert file: %v", err)
		}

	}

	var (
		k8sAuthServiceAccountBearerToken string
	)

	if *fDexClientCertFile != "" && *fDexClientKeyFile != "" && *fDexAPIHost != "" {
		var err error

		if srv.DexClient, err = auth.NewDexClient(*fDexAPIHost, *fDexClientCAFile, *fDexClientCertFile, *fDexClientKeyFile); err != nil {
			log.Fatalf("Failed to create a Dex API client: %v", err)
		}
	}

	var secureCookies bool
	if baseURL.Scheme == "https" {
		secureCookies = true
		log.Info("cookies are secure!")
	} else {
		secureCookies = false
		log.Warning("cookies are not secure because base-address is not https!")
	}

	switch *fK8sMode {
	case "in-cluster":
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if len(host) == 0 || len(port) == 0 {
			log.Fatalf("unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined")
		}
		var err error
		k8sCertPEM, err = ioutil.ReadFile(k8sInClusterCA)
		if err != nil {
			log.Fatalf("Error inferring Kubernetes config from environment: %v", err)
		}
		rootCAs := x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(k8sCertPEM) {
			log.Fatalf("No CA found for the API server")
		}
		tlsConfig := &tls.Config{RootCAs: rootCAs}

		bearerToken, err := ioutil.ReadFile(k8sInClusterBearerToken)
		if err != nil {
			log.Fatalf("failed to read bearer token: %v", err)
		}

		srv.K8sProxyConfig = &proxy.Config{
			TLSClientConfig: tlsConfig,
			HeaderBlacklist: []string{"Cookie"},
			Endpoint:        &url.URL{Scheme: "https", Host: host + ":" + port},
		}

		k8sAuthServiceAccountBearerToken = string(bearerToken)
	case "off-cluster":
		k8sModeOffClusterEndpointURL := validateFlagIsURL("k8s-mode-off-cluster-endpoint", *fK8sModeOffClusterEndpoint)

		srv.K8sProxyConfig = &proxy.Config{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: *fK8sModeOffClusterSkipVerifyTLS,
			},
			HeaderBlacklist: []string{"Cookie"},
			Endpoint:        k8sModeOffClusterEndpointURL,
		}
	default:
		flagFatalf("k8s-mode", "must be one of: in-cluster, off-cluster")
	}

	apiServerEndpoint := *fK8sPublicEndpoint
	if apiServerEndpoint == "" {
		apiServerEndpoint = srv.K8sProxyConfig.Endpoint.String()
	}
	srv.KubeAPIServerURL = apiServerEndpoint

	switch *fUserAuth {
	case "oidc", "openshift":
		validateFlagNotEmpty("base-address", *fBaseAddress)

		userAuthOIDCIssuerURL := validateFlagIsURL("user-auth-oidc-client-id", *fUserAuthOIDCIssuerURL)
		validateFlagNotEmpty("user-auth-oidc-client-id", *fUserAuthOIDCClientID)

		if *fUserAuthOIDCClientSecret == "" && *fUserAuthOIDCClientSecretFile == "" {
			fmt.Fprintln(os.Stderr, "Must provide either --user-auth-oidc-client-secret or --user-auth-oidc-client-secret-file")
			os.Exit(1)
		}

		if *fUserAuthOIDCClientSecret != "" && *fUserAuthOIDCClientSecretFile != "" {
			fmt.Fprintln(os.Stderr, "Cannot provide both --user-auth-oidc-client-secret and --user-auth-oidc-client-secret-file")
			os.Exit(1)
		}

		var (
			err                      error
			authLoginErrorEndpoint   = proxy.SingleJoiningSlash(srv.BaseURL.String(), server.AuthLoginErrorEndpoint)
			authLoginSuccessEndpoint = proxy.SingleJoiningSlash(srv.BaseURL.String(), server.AuthLoginSuccessEndpoint)
			oidcClientSecret         = *fUserAuthOIDCClientSecret
			// Abstraction leak required by NewAuthenticator. We only want the browser to send the auth token for paths starting with basePath/api.
			cookiePath  = proxy.SingleJoiningSlash(srv.BaseURL.Path, "/api/")
			refererPath = srv.BaseURL.String()
		)

		scopes := []string{"openid", "email", "profile", "groups"}
		authSource := auth.AuthSourceTectonic

		if *fUserAuth == "openshift" {
			// Scopes come from OpenShift documentation
			// https://docs.openshift.com/container-platform/3.9/architecture/additional_concepts/authentication.html#service-accounts-as-oauth-clients
			//
			// TODO(ericchiang): Support other scopes like view only permissions.
			scopes = []string{"user:full"}
			authSource = auth.AuthSourceOpenShift
		}

		if *fUserAuthOIDCClientSecretFile != "" {
			buf, err := ioutil.ReadFile(*fUserAuthOIDCClientSecretFile)
			if err != nil {
				log.Fatalf("Failed to read client secret file: %v", err)
			}
			oidcClientSecret = string(buf)
		}

		// Config for logging into console.
		oidcClientConfig := &auth.Config{
			AuthSource:   authSource,
			IssuerURL:    userAuthOIDCIssuerURL.String(),
			IssuerCA:     *fCAFile,
			ClientID:     *fUserAuthOIDCClientID,
			ClientSecret: oidcClientSecret,
			RedirectURL:  proxy.SingleJoiningSlash(srv.BaseURL.String(), server.AuthLoginCallbackEndpoint),
			Scope:        scopes,

			ErrorURL:   authLoginErrorEndpoint,
			SuccessURL: authLoginSuccessEndpoint,

			CookiePath:    cookiePath,
			RefererPath:   refererPath,
			SecureCookies: secureCookies,
		}

		// NOTE: This won't work when using the OpenShift auth mode.
		if *fKubectlClientID != "" {
			srv.KubectlClientID = *fKubectlClientID

			// Assume kubectl is the client ID trusted by kubernetes, not bridge.
			// These additional flags causes Dex to issue an ID token valid for
			// both bridge and kubernetes.
			//
			// For design see: https://github.com/coreos-inc/tectonic/blob/master/docs-internal/tectonic-identity.md
			oidcClientConfig.Scope = append(
				oidcClientConfig.Scope,
				"audience:server:client_id:"+*fUserAuthOIDCClientID,
				"audience:server:client_id:"+*fKubectlClientID,
			)

			kubectlClientSecret := *fKubectlClientSecret
			if *fKubectlClientSecretFile != "" {
				buf, err := ioutil.ReadFile(*fKubectlClientSecretFile)
				if err != nil {
					log.Fatalf("Failed to read client secret file: %v", err)
				}
				kubectlClientSecret = string(buf)
			}

			// Configure an OpenID Connect config for kubectl. This lets us issue
			// refresh tokens that kubectl can redeem using its own credentials.
			kubectlAuthConfig := &auth.Config{
				IssuerURL:    userAuthOIDCIssuerURL.String(),
				IssuerCA:     *fCAFile,
				ClientID:     *fKubectlClientID,
				ClientSecret: kubectlClientSecret,
				// The magic "out of band" redirect URL.
				RedirectURL: "urn:ietf:wg:oauth:2.0:oob",
				// Request a refresh token with the "offline_access" scope.
				Scope: []string{"openid", "email", "profile", "offline_access", "groups"},

				ErrorURL:   authLoginErrorEndpoint,
				SuccessURL: authLoginSuccessEndpoint,

				CookiePath:    cookiePath,
				RefererPath:   refererPath,
				SecureCookies: secureCookies,
			}

			if srv.KubectlAuther, err = auth.NewAuthenticator(context.Background(), kubectlAuthConfig); err != nil {
				log.Fatalf("Error initializing kubectl authenticator: %v", err)
			}

			srv.KubeConfigTmpl = server.NewKubeConfigTmpl(
				*fTectonicClusterName,
				*fKubectlClientID,
				*fKubectlClientSecret,
				apiServerEndpoint,
				userAuthOIDCIssuerURL.String(),
				k8sCertPEM,
				dexCertPEM,
			)
		}

		if srv.Auther, err = auth.NewAuthenticator(context.Background(), oidcClientConfig); err != nil {
			log.Fatalf("Error initializing OIDC authenticator: %v", err)
		}
	case "disabled":
		log.Warningf("running with AUTHENTICATION DISABLED!")
	default:
		flagFatalf("user-auth", "must be one of: oidc, disabled")
	}

	srv.NamespaceLister = &server.ResourceLister{
		BearerToken:   k8sAuthServiceAccountBearerToken,
		ResourcesPath: "/api/v1/namespaces",
		K8sEndpoint:   strings.TrimSuffix(srv.K8sProxyConfig.Endpoint.String(), "/"),
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: srv.K8sProxyConfig.TLSClientConfig,
			},
		},
	}

	srv.CustomResourceDefinitionLister = &server.ResourceLister{
		BearerToken:   k8sAuthServiceAccountBearerToken,
		ResourcesPath: "/apis/apiextensions.k8s.io/v1beta1/customresourcedefinitions",
		K8sEndpoint:   strings.TrimSuffix(srv.K8sProxyConfig.Endpoint.String(), "/"),
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: srv.K8sProxyConfig.TLSClientConfig,
			},
		},
	}

	switch *fK8sAuth {
	case "service-account":
		validateFlagIs("k8s-mode", *fK8sMode, "in-cluster")
		srv.StaticUser = &auth.User{
			Token: k8sAuthServiceAccountBearerToken,
		}
	case "bearer-token":
		validateFlagNotEmpty("k8s-auth-bearer-token", *fK8sAuthBearerToken)
		srv.StaticUser = &auth.User{
			Token: *fK8sAuthBearerToken,
		}
	case "oidc", "openshift":
		validateFlagIs("user-auth", *fUserAuth, "oidc", "openshift")
	default:
		flagFatalf("k8s-mode", "must be one of: service-account, bearer-token, oidc")
	}

	listenURL := validateFlagIsURL("listen", *fListen)
	switch listenURL.Scheme {
	case "http":
	case "https":
		validateFlagNotEmpty("tls-cert-file", *fTlSCertFile)
		validateFlagNotEmpty("tls-key-file", *fTlSKeyFile)
	default:
		flagFatalf("listen", "scheme must be one of: http, https")
	}

	httpsrv := &http.Server{
		Addr:    listenURL.Host,
		Handler: srv.HTTPHandler(),
	}

	log.Infof("Binding to %s...", httpsrv.Addr)
	if listenURL.Scheme == "https" {
		log.Info("using TLS")
		log.Fatal(httpsrv.ListenAndServeTLS(*fTlSCertFile, *fTlSKeyFile))
	} else {
		log.Info("not using TLS")
		log.Fatal(httpsrv.ListenAndServe())
	}
}

func validateFlagIsURL(name string, value string) *url.URL {
	validateFlagNotEmpty(name, value)

	ur, err := url.Parse(value)
	if err != nil {
		flagFatalf(name, "%v", err)
	}

	if ur == nil || ur.String() == "" || ur.Scheme == "" || ur.Host == "" {
		flagFatalf(name, "malformed URL")
	}

	return ur
}

func validateFlagNotEmpty(name string, value string) string {
	if value == "" {
		flagFatalf(name, "value is required")
	}

	return value
}

func validateFlagIs(name string, value string, expectedValues ...string) string {
	if len(expectedValues) != 1 {
		for _, v := range expectedValues {
			if v == value {
				return value
			}
		}
		flagFatalf(name, "value must be one of %s, not %s", expectedValues, value)
	}
	if value != expectedValues[0] {
		flagFatalf(name, "value must be %s, not %s", expectedValues[0], value)
	}

	return value
}

func flagFatalf(name string, format string, a ...interface{}) {
	log.Fatalf("Invalid flag: %s, error: %s", name, fmt.Sprintf(format, a...))
}
