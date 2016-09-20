package client

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"text/template"
	"time"
)

type ServerInfo struct {
	PingSecond time.Duration
}

func (client *Client) getServerInfo() (*ServerInfo, error) {
	client.muServerInfo.Lock()
	defer client.muServerInfo.Unlock()

	if client.serverInfo == nil {
		body, err := client.fetch("GET", HOST_INFO)
		if err != nil {
			return nil, err
		}

		var info ServerInfo
		err = json.Unmarshal(body, &info)
		if err != nil {
			return nil, err
		}

		client.serverInfo = &info
	}

	return client.serverInfo, nil
}

func (client *Client) FetchPac(update bool) (*template.Template, error) {
	host := HOST_PAC
	if update {
		host = HOST_PAC_UPDATE
	}
	body, err := client.fetch("GET", host)
	if err != nil {
		return nil, err
	}

	return template.New("pac").Parse(string(body))
}

func (client *Client) fetch(method, host string) ([]byte, error) {
	req := client.innerRequest(method, host)
	res, err := client.h2Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Should get 200")
	}

	return ioutil.ReadAll(res.Body)
}

func (client *Client) innerRequest(method, host string) *http.Request {
	return &http.Request{
		Method: method,
		URL: &url.URL{
			Scheme: "https",
			Host:   client.ServerUrl.Host,
			Path:   "/",
		},
		Host:       host,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
}
