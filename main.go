package main

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	smtpPort    = 25
	smtpTLSPort = 465
	dnsPort     = 53
	dnsServer   = "1.1.1.1"
)

var (
	defaultDialer = &net.Dialer{
		Timeout: time.Second * 5,
	}

	dnsResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, "udp", fmt.Sprintf("%s:%d", dnsServer, dnsPort))
		},
	}
)

func lookupMX(domain string) (servers []string, err error) {
	mxRecords, err := dnsResolver.LookupMX(context.Background(), domain)
	if err != nil {
		return []string{}, err
	}

	for _, mx := range mxRecords {
		servers = append(servers, mx.Host)
	}

	return servers, nil
}

func extractDomain(email string) (domain string, err error) {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", errors.New("invalid email address")
	}

	return parts[1], nil
}

func checkMailbox(fromDomain, fromEmail, checkEmail string, servers []string) (err error) {
	var smtpClient *smtp.Client

	// try to find a valid mx server to use
	for _, mx := range servers {
		/*
			conn, err := tls.DialWithDialer(
				defaultDialer, "tcp", fmt.Sprintf("%s:%d", mx, smtpTLSPort),
				&tls.Config {
					InsecureSkipVerify: true,
					ServerName: mx,
				},
			)
		*/

		conn, err := defaultDialer.Dial("tcp", fmt.Sprintf("%s:%d", mx, smtpPort))
		if err != nil {
			log.Debugf("skipping %s: %v", mx, err)
			continue
		}

		smtpClient, err = smtp.NewClient(conn, mx)
		if err != nil {
			log.Warnf("could not setup smtp client for %s: %v", mx, err)
			continue
		}
	}

	// if no mx server was found, error out
	if smtpClient == nil {
		return errors.New("no working mail servers could be found")
	}

	defer func() {
		_ = smtpClient.Close()
		_ = smtpClient.Quit()
	}()

	err = smtpClient.Hello(fromDomain)
	if err != nil {
		return errors.Wrap(err, "could not HELO smtp server")
	}

	err = smtpClient.Mail(fromEmail)
	if err != nil {
		return errors.Wrap(err, "could not MAIL FROM smtp server")
	}

	// RCPT TO
	id, err := smtpClient.Text.Cmd("RCPT TO:<%s>", checkEmail)
	if err != nil {
		return errors.Wrap(err, "could not MAIL FROM smtp server")
	}

	smtpClient.Text.StartResponse(id)
	code, _, err := smtpClient.Text.ReadResponse(25)
	smtpClient.Text.EndResponse(id)

	if code == 554 {
		return errors.New("appears our IP is blacklisted")
	}

	// seems to be invalid email
	if code == 550 {
		return errors.New("email does not seem to exist (or server blocks detection)")
	}

	// seems to be valid email
	if code == 250 {
		return nil
	}

	log.Warnf("unknown code returned: %d", code)

	if err != nil {
		return errors.Wrap(err, "smtp response error")
	}

	return nil
}

func main() {
	log.SetLevel(log.DebugLevel)

	emails := os.Args[1:]
	if len(emails) == 0 {
		log.Fatalf("usage: %s email ...", filepath.Base(os.Args[0]))
	}

	for _, email := range emails {
		emailDomain, err := extractDomain(email)
		if err != nil {
			log.Errorf("could not extract domain: %v", err)
			if len(emails) == 1 {
				os.Exit(1)
			} else {
				continue
			}
		}

		mxServers, err := lookupMX(emailDomain)
		if err != nil {
			log.Errorf("could not retrieve mail server: %v", err)
			if len(emails) == 1 {
				os.Exit(1)
			} else {
				continue
			}
		}

		if len(mxServers) == 0 {
			log.Infof("no mail servers found for %s", email)
			if len(emails) == 1 {
				os.Exit(0)
			} else {
				continue
			}
		}

		if err := checkMailbox("ironpeak.be", "test@ironpeak.be", email, mxServers); err != nil {
			log.Infof("seems to be invalid (%s)", err)
			if len(emails) == 1 {
				os.Exit(1)
			}
		} else {
			log.Infof("seems to be valid")
		}
	}
}
