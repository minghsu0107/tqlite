package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

func removeNode(client *http.Client, id string, argv *argT, timer bool) error {
	u := url.URL{
		Scheme: argv.Protocol,
		Host:   fmt.Sprintf("%s:%d", argv.Host, argv.Port),
		Path:   fmt.Sprintf("%sremove", argv.Prefix),
	}
	urlStr := u.String()

	b, err := json.Marshal(map[string]string{
		"id": id,
	})
	if err != nil {
		return err
	}

	nRedirect := 0
	for {
		req, err := http.NewRequest("DELETE", urlStr, bytes.NewReader(b))
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("unauthorized")
		}

		if resp.StatusCode == http.StatusMovedPermanently {
			nRedirect++
			if nRedirect > maxRedirect {
				return fmt.Errorf("maximum leader redirect limit exceeded")
			}
			urlStr = resp.Header["Location"][0]
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("server responded with: %s", resp.Status)
		}

		return nil
	}
}
