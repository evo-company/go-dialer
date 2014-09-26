package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/parnurzeal/gorequest"
	"github.com/vmihailenco/signer"
	"github.com/warik/gami"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/dialer/model"
)

const (
	INCOMING_CALL = iota
	OUTGOING_CALL
	INNER_CALL
	UNKNOWN_CALL
	INCOMING_CALL_HIDDEN
)

var InnerPhonesNumber map[string]map[string]string

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func getKey(secret string) []byte {
	sh := sha1.New()
	sh.Write([]byte("saltysigner" + secret))
	return sh.Sum(nil)
}

func signData(m map[string]string, secret string) (signedData string, err error) {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, getKey(secret))

	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	signedData = string(signer.NewBase64Signer(h).Sign(data))
	return
}

func getInnerNumbers() {
	temp := map[string]map[string]string{}
	for countryCode, settings := range conf.GetConf().Agencies {
		url := conf.GetConf().GetApi(countryCode, "get_employees_inner_phone")
		numbers, err := SendRequest(map[string]string{}, url, "GET", settings.Secret,
			settings.CompanyId)
		if err != nil {
			glog.Errorln(err)
			continue
		}
		temp[countryCode] = map[string]string{}
		for _, number := range strings.Split(numbers, ",") {
			temp[countryCode][number] = ""
		}
	}
	InnerPhonesNumber = temp
	glog.Infoln("Inner numbers", InnerPhonesNumber)
}

func GetPhoneDetails(m gami.Message) (string, string, int) {
	re, _ := regexp.Compile("^\\w+/(\\d{2,4}|\\d{4}\\w{2})\\D*-.+$")
	in := re.FindStringSubmatch(m["Channel"])
	out := re.FindStringSubmatch(m["DestinationChannel"])
	if in != nil && out != nil {
		// If both phones are inner and same - its incoming call through queue
		// If not - inner call
		if in[1] == out[1] {
			// If there is no any form of source - its hidden call
			if m["Source"] == "" && m["CallerID"] == "" {
				return out[1], "xxxx", INCOMING_CALL_HIDDEN
			} else {
				return out[1], m["Source"], INCOMING_CALL
			}
		} else {
			return "", "", INNER_CALL
		}
	}
	if in != nil {
		return in[1], m["Destination"], OUTGOING_CALL
	}
	if out != nil {
		if m["Source"] == "" && m["CallerID"] == "" {
			return out[1], "xxxx", INCOMING_CALL_HIDDEN
		} else {
			return out[1], m["Source"], INCOMING_CALL
		}
	} else {
		// High chances that this is just dropped phone, so just ignore
		return "", "", -1
	}
}

func GetCallback() (cb func(gami.Message), cbc chan gami.Message) {
	cbc = make(chan gami.Message)
	cb = func(m gami.Message) {
		cbc <- m
	}
	return
}

func WriteResponse(
	w http.ResponseWriter,
	resp gami.Message,
	statusFromResponse bool,
	dataKey string,
) {
	glog.Infoln("Response", resp)

	var status string
	if statusFromResponse {
		status = strings.ToLower(resp["Response"])
	}

	response := ""
	if val, ok := resp[dataKey]; ok {
		response = val
		status = "success"
	} else {
		status = "error"
	}
	fmt.Fprint(w, model.Response{"status": status, "response": response})
}

func UnsignData(i interface{}, d model.SignedInputData) error {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, []byte(getKey(conf.GetConf().Agencies[d.Country].Secret)))
	dataString, ok := signer.NewBase64Signer(h).Verify([]byte(d.Data))

	if !ok {
		return errors.New(fmt.Sprintf("Bad signature - %s", strings.Split(d.Data, ".")[1]))
	}

	return json.Unmarshal(dataString, &i)
}

func Clean(finishChannels []chan struct{}, wg *sync.WaitGroup) {
	for _, channel := range finishChannels {
		close(channel)
	}
	wg.Wait()
	db.GetDB().Close()
	ami.GetAMI().Logoff()
	// panic("Manual panic for checking goroutines")
}

func SendRequest(m map[string]string, url, method, secret, companyId string) (string, error) {
	m["CompanyId"] = companyId
	signedData, err := signData(m, secret)
	if err != nil {
		return "", err
	}

	data := model.SignedData{Data: signedData, CompanyId: companyId}
	request := gorequest.New()
	if method == "POST" {
		request.Post(url).Send(data)
	} else if method == "GET" {
		query, _ := json.Marshal(data)
		request.Get(url).Query(string(query))
	}

	resp, respBody, errs := request.Timeout(conf.REQUEST_TIMEOUT * time.Second).End()

	if len(errs) != 0 {
		return "", errs[0]
	}

	if resp.StatusCode != 200 {
		return "", errors.New(fmt.Sprintf(conf.REMOTE_ERROR_TEXT, resp.StatusCode))
	}

	return respBody, nil
}
