package main

import (
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/dgnorton/faxme/accounts"
	"github.com/kevinburke/twilio-go"
	"go.uber.org/zap"
)

const (
	DefaultConfigFile      = "/etc/faxme/faxme.toml"
	DefaultHTTPBindAddress = "127.0.0.1"
	DefaultHTTPPort        = "7500"
)

func main() {
	var cfgFile string
	cmdlineCfg := NewConfig()

	flag.StringVar(&cfgFile, "config", "", "config file path")
	flag.StringVar(&cmdlineCfg.HTTPBindAddress, "http", "", "HTTP bind address")
	flag.StringVar(&cmdlineCfg.HTTPPort, "port", "", "HTTP port")
	flag.StringVar(&cmdlineCfg.HTTPUser, "user", "", "username expected when authenticating HTTP requests")
	flag.StringVar(&cmdlineCfg.HTTPPwd, "pwd", "", "user password expected when authenticating HTTP requests")
	flag.StringVar(&cmdlineCfg.TLSCertFile, "tlscert", "", "path to TLS cert file")
	flag.StringVar(&cmdlineCfg.TLSKeyFile, "tlskey", "", "path to TLS key file")
	flag.BoolVar(&cmdlineCfg.Unsafe, "unsafe", false, "must be specified if not using TLS or basic auth")
	flag.StringVar(&cmdlineCfg.FaxNumber, "fax", "", "your fax number")
	flag.StringVar(&cmdlineCfg.SMSNumber, "sms", "", "your mobile number")
	flag.StringVar(&cmdlineCfg.AccountsFile, "accounts", "", "fax account file")
	flag.StringVar(&cmdlineCfg.TwilioSID, "sid", "", "Twilio SID")
	flag.StringVar(&cmdlineCfg.TwilioToken, "token", "", "Twilio token")
	flag.BoolVar(&cmdlineCfg.SkipRequestValidation, "skip-req-val", false, "skips HTTP request validation")
	flag.Parse()

	cfg, err := readConfig(cfgFile, cmdlineCfg)
	check(err)

	err = newServer(cfg).run()
	check(err)
}

type server struct {
	cfg   *Config
	log   *zap.Logger
	logid int64

	mu    sync.RWMutex
	acnts *accounts.Accounts
}

func newServer(cfg *Config) *server {
	log, _ := zap.NewProduction()
	return &server{
		cfg: cfg,
		log: log,
	}
}

func (s *server) nextLogID() int64 {
	return atomic.AddInt64(&s.logid, 1)
}

func (s *server) loadAccounts() error {
	//s.log.Info("load accounts", zap.String("path", s.cfg.AccountsFile))
	acnts := accounts.NewAccounts()
	var err error

	if s.cfg.AccountsFile != "" {
		if acnts, err = accounts.ReadFile(s.cfg.AccountsFile); err != nil {
			return err
		}
	}

	if s.cfg.FaxNumber != "" && s.cfg.SMSNumber != "" {
		acnts.Add(s.cfg.FaxNumber, []string{s.cfg.SMSNumber})
	}

	s.mu.Lock()
	s.acnts = acnts
	s.mu.Unlock()

	return nil
}

func (s *server) run() error {
	logid := s.nextLogID()
	s.log.Info("faxme server started", zap.Int64("log_id", logid))
	defer s.log.Info("faxme server stopped", zap.Int64("log_id", logid))

	// Make sure a Twilio SID has been provided in configuration.
	if s.cfg.TwilioSID == "" {
		err := errors.New("Twilio SID must be configured")
		s.log.Info(err.Error())
		return err
	}

	// Ensure Twilio Token has been provided in configuration.
	if s.cfg.TwilioToken == "" {
		err := errors.New("Twilio Token must be configured")
		s.log.Info(err.Error())
		return err
	}

	every := time.Second * 10

	s.log.Info("load accounts", zap.String("every", every.String()), zap.String("path", s.cfg.AccountsFile))
	if err := s.loadAccounts(); err != nil {
		s.log.Info("failed load accounts", zap.String("path", s.cfg.AccountsFile), zap.Error(err))
		return err
	}

	// Start a goroutine to periodically reload accounts.
	go func() {
		ch := time.Tick(every)
		for _ = range ch {
			s.loadAccounts()
		}
	}()

	hndlFaxReceive := s.basicAuth(s.serveFaxReceive, s.cfg.HTTPUser, s.cfg.HTTPPwd, "faxme")
	hndlFaxReceived := s.basicAuth(s.serveFaxReceived, s.cfg.HTTPUser, s.cfg.HTTPPwd, "faxme")

	if s.cfg.HTTPUser == "" || s.cfg.HTTPPwd == "" {
		if !s.cfg.Unsafe {
			err := errors.New("provide username and password in config or specify '-unsafe' command line option")
			s.log.Error(err.Error())
			return err
		}
		s.log.Warn("WARNING: no user credentials provided in configuration and running in unsafe mode")
		hndlFaxReceive = s.serveFaxReceive
		hndlFaxReceived = s.serveFaxReceived
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/fax/receive", hndlFaxReceive)
	mux.HandleFunc("/fax/received", hndlFaxReceived)

	addr := fmt.Sprintf("%s:%s", s.cfg.HTTPBindAddress, s.cfg.HTTPPort)

	if s.cfg.TLSCertFile == "" && s.cfg.TLSKeyFile == "" {
		if !s.cfg.Unsafe {
			err := errors.New("provide TLS cert and key file in config or specify '-unsafe' command line option")
			s.log.Error(err.Error())
			return err
		}

		s.log.Warn("WARNING: unencrypted HTTP server listening", zap.String("addr", addr))
		return http.ListenAndServe(addr, mux)
	}

	s.log.Info("HTTPS server listening", zap.String("addr", addr))
	return http.ListenAndServeTLS(addr, s.cfg.TLSCertFile, s.cfg.TLSKeyFile, mux)
}

func (s *server) serveFaxReceive(w http.ResponseWriter, r *http.Request) {
	logid := s.nextLogID()
	s.log.Info("HTTP request begin", zap.String("method", r.Method), zap.String("url", r.URL.String()), zap.Int64("log_id", logid))
	defer s.log.Info("HTTP request end", zap.Int64("log_id", logid))

	if r.Method != "POST" {
		s.httpErr(w, http.StatusNotFound, "", logid)
		return
	}

	if !s.cfg.SkipRequestValidation {
		if err := twilio.ValidateIncomingRequest(s.cfg.HTTPBindAddress, s.cfg.TwilioToken, r); err != nil {
			s.log.Info("Twilio request validation failed", zap.Error(err), zap.Int64("log_id", logid))
			s.httpErr(w, http.StatusUnauthorized, "", logid)
			return
		}
	}

	qry := r.URL.Query()

	to := qry.Get("to")
	if to == "" {
		s.httpErr(w, http.StatusBadRequest, `missing "?to=<faxnumber>" query in reuest`, logid)
		return
	}

	resp := "<Response><Reject/></Response>"

	acnt := s.findAccount(to)
	if acnt != nil && acnt.FaxNumber == to {
		resp = fmt.Sprintf(`<Response><Receive action="/fax/received?to=%s"/></Response>`, to)
	}

	s.log.Info("HTTP response", zap.String("fax_num", to), zap.String("resp", resp), zap.Int64("log_id", logid))

	w.Write([]byte(resp))
}

func (s *server) serveFaxReceived(w http.ResponseWriter, r *http.Request) {
	logid := s.nextLogID()
	s.log.Info("HTTP request begin", zap.String("method", r.Method), zap.String("url", r.URL.String()), zap.Int64("log_id", logid))
	defer s.log.Info("HTTP request end", zap.Int64("log_id", logid))

	if r.Method != "POST" {
		s.httpErr(w, http.StatusNotFound, "", logid)
		return
	}

	if !s.cfg.SkipRequestValidation {
		if err := twilio.ValidateIncomingRequest(s.cfg.HTTPBindAddress, s.cfg.TwilioToken, r); err != nil {
			s.log.Info("Twilio request validation failed", zap.Error(err), zap.Int64("log_id", logid))
			s.httpErr(w, http.StatusUnauthorized, "", logid)
			return
		}
	}

	qry := r.URL.Query()

	to := qry.Get("to")
	if to == "" {
		s.httpErr(w, http.StatusBadRequest, `missing "?to=<faxnumber>" query in reuest`, logid)
		return
	}

	acnt := s.findAccount(to)
	if acnt == nil || acnt.FaxNumber != to {
		s.log.Info("account not found", zap.String("fax_num", to), zap.Int64("log_id", logid))
		return
	}

	url := r.FormValue("MediaUrl")
	if url == "" {
		s.log.Info("mising MediaUrl", zap.String("fax_num", to), zap.Int64("log_id", logid))
		return
	}

	msg := fmt.Sprintf("You have a fax!\n\n%s", url)

	for i, _ := range acnt.Contacts {
		if err := s.sendSMS(acnt.FaxNumber, acnt.Contacts[i], msg); err != nil {
			s.log.Info("failed SMS", zap.String("fax_num", to), zap.String("contact", acnt.Contacts[i]), zap.Error(err), zap.Int64("log_id", logid))
		}
	}
}

// findAccount returns the account associated with a fax number.
func (s *server) findAccount(fax string) *accounts.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.acnts.Find(fax)
}

func (s *server) basicAuth(handler http.HandlerFunc, username, password, realm string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		user, pass, ok := r.BasicAuth()

		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			s.log.Info("HTTP error", zap.Int("status", http.StatusUnauthorized))
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Unauthorised.\n"))
			return
		}

		handler(w, r)
	}
}

func (s *server) httpErr(w http.ResponseWriter, status int, msg string, logID int64) {
	s.log.Info("HTTP error", zap.Int("status", status), zap.String("msg", msg), zap.Int64("log_id", logID))
	w.WriteHeader(status)
	w.Write([]byte(msg))
}

type Config struct {
	HTTPBindAddress       string `toml:"http-bind-address"`
	HTTPPort              string `toml:"http-port"`
	HTTPUser              string `toml:"http-user"`
	HTTPPwd               string `toml:"http-pwd"`
	TLSCertFile           string `toml:"tls-cert-file"`
	TLSKeyFile            string `toml:"tls-key-file"`
	Unsafe                bool
	FaxNumber             string `toml:"fax-number"`
	SMSNumber             string `toml:"mobile-number"`
	AccountsFile          string `toml:"accounts-file"`
	TwilioSID             string `toml:"twilio-sid"`
	TwilioToken           string `toml:"twilio-token"`
	SkipRequestValidation bool
}

func NewConfig() *Config {
	return &Config{
		HTTPBindAddress: DefaultHTTPBindAddress,
		HTTPPort:        DefaultHTTPPort,
	}
}

// readConfig reads configs from lowest to highest priority. Higher priority
// overwrites lower priority. The order from low to high is:
//   1. file
//   2. environment
//   3. command line
func readConfig(path string, cmdlineCfg *Config) (*Config, error) {
	// Get a default config.
	c := NewConfig()

	// Overlay file config.
	println(path)
	_, err := toml.DecodeFile(path, c)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Overlay environment config.
	c.HTTPBindAddress, _ = getenv("FAXME_HTTP_BIND_ADDRESS", c.HTTPBindAddress)
	c.HTTPPort, _ = getenv("FAXME_HTTP_PORT", c.HTTPPort)
	c.HTTPUser, _ = getenv("FAXME_HTTP_USER", c.HTTPUser)
	c.HTTPPwd, _ = getenv("FAXME_HTTP_PWD", c.HTTPPwd)
	c.TLSCertFile, _ = getenv("FAXME_TLS_CERT_FILE", c.TLSCertFile)
	c.TLSKeyFile, _ = getenv("FAXME_TLS_KEY_FILE", c.TLSKeyFile)
	c.FaxNumber, _ = getenv("FAXME_FAX_NUMBER", c.FaxNumber)
	c.SMSNumber, _ = getenv("FAXME_MOBILE_NUMBER", c.SMSNumber)
	c.AccountsFile, _ = getenv("FAXME_ACCOUNTS_FILE", c.AccountsFile)
	c.TwilioSID, _ = getenv("FAXME_TWILIO_SID", c.TwilioSID)
	c.TwilioToken, _ = getenv("FAXME_TWILIO_TOKEN", c.TwilioToken)

	// Overlay command line config.
	c.HTTPBindAddress = overlay(c.HTTPBindAddress, cmdlineCfg.HTTPBindAddress)
	c.HTTPPort = overlay(c.HTTPPort, cmdlineCfg.HTTPPort)
	c.HTTPUser = overlay(c.HTTPUser, cmdlineCfg.HTTPUser)
	c.HTTPPwd = overlay(c.HTTPPwd, cmdlineCfg.HTTPPwd)
	c.TLSCertFile = overlay(c.TLSCertFile, cmdlineCfg.TLSCertFile)
	c.TLSKeyFile = overlay(c.TLSKeyFile, cmdlineCfg.TLSKeyFile)
	c.Unsafe = cmdlineCfg.Unsafe
	c.FaxNumber = overlay(c.FaxNumber, cmdlineCfg.FaxNumber)
	c.SMSNumber = overlay(c.SMSNumber, cmdlineCfg.SMSNumber)
	c.AccountsFile = overlay(c.AccountsFile, cmdlineCfg.AccountsFile)
	c.TwilioSID = overlay(c.TwilioSID, cmdlineCfg.TwilioSID)
	c.TwilioToken = overlay(c.TwilioToken, cmdlineCfg.TwilioToken)
	c.SkipRequestValidation = cmdlineCfg.SkipRequestValidation

	return c, nil
}

func overlay(old, new string) string {
	if new != "" {
		return new
	}
	return old
}

func (s *server) sendSMS(from, to, msg string) error {
	twil := twilio.NewClient(s.cfg.TwilioSID, s.cfg.TwilioToken, http.DefaultClient)
	_, err := twil.Messages.SendMessage(from, to, msg, nil)
	return err
}

type TwilioCreds struct {
	SID   string
	Token string
}

func twilioCredsFromEnv() (*TwilioCreds, error) {
	sid, err := getenv("FAXME_TWILIO_SID", "")
	if err != nil {
		return nil, err
	}

	tok, err := getenv("FAXME_TWILIO_TOKEN", "")
	if err != nil {
		return nil, err
	}

	return &TwilioCreds{sid, tok}, nil
}

func getenv(key, defalt string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		if defalt == "" {
			return "", fmt.Errorf("environment variable %q not set", key)
		}
		val = defalt
	}
	return val, nil
}

func getenvBool(key, defalt string) (bool, error) {
	s, err := getenv(key, defalt)
	if err != nil {
		return false, err
	}

	s = strings.ToLower(s)

	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("can't convert %q to bool", s)
	}
}

func check(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
