// Copyright © 2016-2017 Martin Tournoij <martin@arp242.net>
// See the bottom of this file for the full copyright notice.
package main // import "arp242.net/transip-dynamic"

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"arp242.net/sconfig"
)

type configT struct {
	User    string
	KeyFile string
	API     string
	GetIP   string
	Records map[string][]string

	key *rsa.PrivateKey
}

type ipT struct {
	IPv6 string
	IPv4 string
}

// Domain is a domain we want to update.
//type Domain struct {
//	// DOmain name; e.g. example.com
//	Domain string
//
//	// FQDNs we want to update; e.g. www.example.com or example.com
//	FQDNs []string
//
//	// List of currently set domains from the API; we need to send back *ALL*
//	// domains, not just the one we want to update.
//	Records []Info
//}

const (
	version    = "5.2"
	mode       = "readwrite"
	soapHeader = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope
	xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"
	xmlns:ns1="http://www.transip.nl/soap"
	xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	xmlns:SOAP-ENC="http://schemas.xmlsoap.org/soap/encoding/"
	SOAP-ENV:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"
>
	<SOAP-ENV:Body>`
)

var config configT

func main() {
	path := ""
	flag.StringVar(&path, "config", "",
		"path to config file; default: ./config")
	flag.Parse()

	err := parseConfig(path)
	fatal(err)

	err = updateDomains()
	fatal(err)
}

func fatal(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "transip-dynamic error: %v\n", err)
	os.Exit(1)
}

func parseConfig(path string) error {
	if path == "" {
		path = "config"
	}

	// Parse config
	return sconfig.Parse(&config, path, sconfig.Handlers{
		"KeyFile": func(v []string) (err error) {
			config.KeyFile = strings.Join(v, " ")
			config.key, err = readKey(config.KeyFile)
			if err != nil {
				return err
			}
			return nil
		},
		"Records": func(v []string) (err error) {
			if config.Records == nil {
				config.Records = make(map[string][]string)
			}
			for _, r := range v {
				r = strings.TrimRight(r, ".")
				s := strings.Split(r, ".")
				if len(s) < 2 {
					return fmt.Errorf("record %v doesn't look like a valid FQDN", r)
				}

				domain := strings.Join(s[len(s)-2:], ".")
				FQDNs := []string{r + "."}

				config.Records[domain] = append(config.Records[domain], FQDNs...)
			}

			return nil
		},
	})
}

func readKey(file string) (*rsa.PrivateKey, error) {
	fp, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	data, err := ioutil.ReadAll(fp)
	if err != nil {
		return nil, err
	}

	pemKey, _ := pem.Decode(data)
	rsaKey, err := x509.ParsePKCS8PrivateKey(pemKey.Bytes)
	if err != nil {
		return nil, err
	}

	return rsaKey.(*rsa.PrivateKey), nil
}

// updateDomains gets all the domain info from the API for the domains in
// config.Records. It will also update the records to the new value(s)
func updateDomains() error {
	ip, err := getIP()
	if err != nil {
		return err
	}

	for domain, records := range config.Records {
		info, err := getDomain(domain)
		if err != nil {
			return fmt.Errorf("cannot get domain %v: %v", domain, err)
		}

		err = updateDomain(domain, records, info, *ip)
		if err != nil {
			return fmt.Errorf("cannot update domain %v: %v", domain, err)
		}
	}

	return nil
}

// getIP gets the current public IP address
func getIP() (*ipT, error) {
	addrs, err := net.LookupHost(config.GetIP)
	if err != nil {
		return nil, err
	}

	get := func(a string) (string, error) {
		client := http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest("GET", fmt.Sprintf("http://[%v]", a), nil)
		if err != nil {
			return "", err
		}

		req.Header.Add("User-Agent", "curl/7.54.0")
		req.Header.Add("Host", config.GetIP)
		req.Header.Add("Accept", "*/*")
		req.Host = config.GetIP
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("cannot read IP: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return "", fmt.Errorf("wrong status code at %v: %v %v",
				a, resp.StatusCode, resp.Status)
		}

		d, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		// Almost certainly wrong!
		if len(d) > 100 {
			return "", fmt.Errorf("data from %v is far too long; bailing out", a)
		}

		return strings.TrimSpace(string(d)), nil
	}

	// Select one IPv4 and one IPv6 address
	ip := &ipT{}
	for _, a := range addrs {
		hasC := strings.Contains(a, ":")
		if ip.IPv6 == "" && hasC {
			addr, err := get(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "transip-dynamic warning: cannot find IPv4 address: %v\n",
					err)
			} else {
				ip.IPv6 = addr
			}
		}

		if ip.IPv4 == "" && !hasC {
			addr, err := get(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "transip-dynamic warning: cannot find IPv6 address: %v\n",
					err)
			} else {
				ip.IPv4 = addr
			}
		}

		if ip.IPv4 != "" && ip.IPv6 != "" {
			break
		}
	}

	if ip.IPv4 == "" && ip.IPv6 == "" {
		fmt.Fprintf(os.Stderr, "transip-dynamic error: no IP addresses found\n")
		os.Exit(1)
	}

	return ip, nil
}

// getDomain gets a single domain from the API
func getDomain(name string) ([]Info, error) {
	data, err := soapRequest("DomainService", "getInfo", []string{name}, fmt.Sprintf(`
		<ns1:getInfo>
			<domainName xsi:type="xsd:string">%v</domainName>
		</ns1:getInfo>`, name))
	if err != nil {
		return nil, err
	}

	// TODO: Error detection ("faultstring"/"faultcode")
	body := MyRespEnvelope{}
	err = xml.Unmarshal(data, &body)
	if err != nil {
		return nil, err
	}

	info := body.Body.GetInfoResponse.Return.DNSEntries.Info
	for i := range info {
		if info[i].Name == "@" {
			info[i].FQDN = name + "."
		} else {
			info[i].FQDN = info[i].Name + "." + name + "."
		}
	}

	return info, nil
}

func updateDomain(domain string, records []string, info []Info, ip ipT) error {
	f := 0
	for _, record := range records {
		for i := range info {
			// Never update these
			if info[i].Type != "A" && info[i].Type != "AAAA" {
				continue
			}

			if record == info[i].FQDN {
				if info[i].Expire > 3600 {
					fmt.Fprintf(os.Stderr, "transip-dynamic warning: TTL for %v is very high (%v seconds)\n",
						record, info[i].Expire)
				}

				if info[i].Type == "A" {
					if ip.IPv4 == "" {
						return fmt.Errorf("no IPv4 address found but %v is an A record",
							record)
					}
					info[i].Content = ip.IPv4
				} else {
					if ip.IPv6 == "" {
						return fmt.Errorf("no IPv6 address found but %v is an AAAA record",
							record)
					}
					info[i].Content = ip.IPv6
				}
				f++
			}
		}
	}
	if len(records) > f {
		return fmt.Errorf("no A or AAAA record found for %v; did you set them in TransIP?",
			records)
	}

	// Now that we have all the updated info send it off to TransIP
	return sendUpdate(domain, info)
}

func sendUpdate(domain string, info []Info) error {
	body := fmt.Sprintf(`
		<ns1:setDnsEntries>
			<domainName xsi:type="xsd:string">%v</domainName>
			<dnsEntries SOAP-ENC:arrayType="ns1:DnsEntry[%v]" xsi:type="ns1:ArrayOfDnsEntry">
	`, domain, len(info))
	params := []string{domain, ""}

	for c, i := range info {
		body += fmt.Sprintf(`
			<item xsi:type="ns1:DnsEntry">
				<name xsi:type="xsd:string">%v</name>
				<expire xsi:type="xsd:int">%v</expire>
				<type xsi:type="xsd:string">%v</type>
				<content xsi:type="xsd:string">%v</content>
			</item>
			`, i.Name, i.Expire, i.Type, i.Content)

		p := fmt.Sprintf("1[%v][name]=%v&", c, url.QueryEscape(i.Name))
		p += fmt.Sprintf("1[%v][expire]=%v&", c, i.Expire)
		p += fmt.Sprintf("1[%v][type]=%v&", c, url.QueryEscape(i.Type))
		p += fmt.Sprintf("1[%v][content]=%v&", c, url.QueryEscape(i.Content))
		params[1] += p
	}
	body += "</dnsEntries></ns1:setDnsEntries>"

	data, err := soapRequest("DomainService", "setDnsEntries", params, body)
	if err != nil {
		return err
	}

	sdata := string(data)

	if strings.Index(sdata, "faultstring") != -1 {
		// TODO: get faultstring out of here
		return errors.New(sdata)
	}
	return nil
}

// All the crap related to parsing XML and SOAP

// soapRequest is a very hacky and ad-hoc SOAP implementation that just happens
// to work with the TransIP API.
func soapRequest(service, method string, params []string, reqBody string) ([]byte, error) {
	req, err := http.NewRequest("POST",
		fmt.Sprintf("https://%v/soap/?service=%v", config.API, service),
		bytes.NewBuffer([]byte(fmt.Sprintf("%v %v </SOAP-ENV:Body> </SOAP-ENV:Envelope>",
			soapHeader, reqBody))))
	if err != nil {
		return nil, err
	}

	now := strconv.FormatInt(time.Now().Unix(), 10)
	b := make([]byte, 4)
	_, err = io.ReadFull(rand.Reader, b)
	if err != nil {
		return nil, err
	}

	nonce := fmt.Sprintf("%x", b)

	urlParams := url.Values{}
	for i, v := range params {
		urlParams.Set(strconv.FormatInt(int64(i), 10), v)
	}
	urlParams.Set("__service", service)
	urlParams.Set("__hostname", config.API)
	urlParams.Set("__timestamp", now)
	urlParams.Set("__nonce", nonce)
	urlParams.Set("__method", method)

	sig, err := sign(config.key, urlParams)
	if err != nil {
		return nil, err
	}

	req.AddCookie(&http.Cookie{Name: "login", Value: config.User})
	req.AddCookie(&http.Cookie{Name: "mode", Value: mode})
	req.AddCookie(&http.Cookie{Name: "timestamp", Value: now})
	req.AddCookie(&http.Cookie{Name: "nonce", Value: nonce})
	req.AddCookie(&http.Cookie{Name: "clientVersion", Value: version})
	req.AddCookie(&http.Cookie{Name: "signature", Value: url.QueryEscape(sig)})
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", fmt.Sprintf("urn:%v#%vServer#%v", service, service, method))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)

	return body, nil
}

func sign(key *rsa.PrivateKey, params url.Values) (string, error) {
	hash := sha512.New()
	if p := params.Get("0"); p != "" {
		hash.Write([]byte(fmt.Sprintf("0=%v&", p)))
	}
	if p := params.Get("1"); p != "" {
		hash.Write([]byte(fmt.Sprintf("%v", p)))
	}

	hash.Write([]byte(fmt.Sprintf("__method=%v&__service=%v&__hostname=%v&__timestamp=%v&__nonce=%v",
		params.Get("__method"), params.Get("__service"),
		params.Get("__hostname"), params.Get("__timestamp"),
		params.Get("__nonce"))))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA512, hash.Sum(nil))
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(sig), nil
}

// Info is a single DNS record as returned from the API
type Info struct {
	Name    string `xml:"name"`
	Expire  int    `xml:"expire"`
	Type    string `xml:"type"`
	Content string `xml:"content"`

	// Added
	FQDN string
}

func (i Info) String() string {
	return fmt.Sprintf("%-24v%-7v IN      %-7v %v",
		i.FQDN, i.Expire, i.Type, i.Content)
}

// MyRespEnvelope is SOAP/XML crap
type MyRespEnvelope struct {
	Body Body
}

// Body is SOAP/XML crap
type Body struct {
	GetInfoResponse GetInfoResponse `xml:"getInfoResponse"`
}

// GetInfoResponse is SOAP/XML crap
type GetInfoResponse struct {
	Return Return `xml:"return"`
}

// Return is SOAP/XML crap
type Return struct {
	DNSEntries DNSEntries `xml:"dnsEntries"`
}

// DNSEntries is SOAP/XML crap
type DNSEntries struct {
	Info []Info `xml:"item"`
}

// The MIT License (MIT)
//
// Copyright © 2016-2017 Martin Tournoij
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to
// deal in the Software without restriction, including without limitation the
// rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
// sell copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// The software is provided "as is", without warranty of any kind, express or
// implied, including but not limited to the warranties of merchantability,
// fitness for a particular purpose and noninfringement. In no event shall the
// authors or copyright holders be liable for any claim, damages or other
// liability, whether in an action of contract, tort or otherwise, arising
// from, out of or in connection with the software or the use or other dealings
// in the software.
