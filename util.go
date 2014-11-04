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
	"time"

	"github.com/golang/glog"
	"github.com/parnurzeal/gorequest"
	"github.com/vmihailenco/signer"
	"github.com/warik/gami"

	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/model"
)

const (
	INCOMING_CALL = iota
	OUTGOING_CALL
	INNER_CALL
	UNKNOWN_CALL
	INCOMING_CALL_HIDDEN
)

type Dict map[string]string

var InnerPhonesNumber map[string]model.Set

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
	temp := map[string]model.Set{}
	for countryCode, settings := range conf.GetConf().Agencies {
		url := conf.GetConf().GetApi(countryCode, "get_employees_inner_phone")
		numbers, err := SendRequest(Dict{}, url, "GET", settings.Secret,
			settings.CompanyId)
		if err != nil {
			glog.Errorln(err)
			continue
		}
		temp[countryCode] = model.Set{}
		for _, number := range strings.Split(numbers, ",") {
			temp[countryCode][number] = struct{}{}
		}
	}
	InnerPhonesNumber = temp
	glog.Infoln("<<< INNER NUMBERS", InnerPhonesNumber)
}

func GetActiveQueuesMap(activeQueuesChan <-chan gami.Message) (
	queuesNumberMap map[string]map[string][]string) {
	queuesNumberMap = make(map[string]map[string][]string)
	for len(activeQueuesChan) > 0 {
		qm := <-activeQueuesChan
		queue, number, country := qm["Queue"], qm["Name"][6:10], qm["Name"][10:12]
		// If its static queue then it doesn't attach to country, so just skip
		if country != "ua" && country != "ru" {
			continue
		}
		if _, ok := queuesNumberMap[country]; !ok {
			queuesNumberMap[country] = make(map[string][]string)
		}
		activeQueues, ok := queuesNumberMap[country][number]
		if !ok {
			queuesNumberMap[country][number] = []string{queue}
		} else {
			queuesNumberMap[country][number] = append(activeQueues, queue)
		}
	}
	return
}

func GetPhoneDetails(channel, destChannel, source, destination, callerId string) (string,
	string, int) {
	re, _ := regexp.Compile("^\\w+/(\\d{2,4}|\\d{4}\\w{2})\\D*-.+$")
	in := re.FindStringSubmatch(channel)
	out := re.FindStringSubmatch(destChannel)
	re = nil
	if in != nil && out != nil {
		// If both phones are inner and same - its incoming call through queue
		// If not - inner call
		if in[1] == out[1] {
			// If there is no any form of source - its hidden call
			if source == "" && callerId == "" {
				return out[1], "xxxx", INCOMING_CALL_HIDDEN
			} else {
				return out[1], source, INCOMING_CALL
			}
		} else {
			return "", "", INNER_CALL
		}
	}
	if in != nil && len(destination) >= 4 {
		return in[1], destination, OUTGOING_CALL
	}
	if out != nil {
		if source == "" && callerId == "" {
			return out[1], "xxxx", INCOMING_CALL_HIDDEN
		} else {
			return out[1], source, INCOMING_CALL
		}
	} else {
		// High chances that this is just dropped phone, so just ignore
		return "", "", -1
	}
}

func APIResponseWriter(resp model.Response, err error, w http.ResponseWriter) {
	if err != nil {
		glog.Errorln(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		glog.Info("<<< PORTAL RESPONSE", resp)
		fmt.Fprint(w, resp)
	}
}

func AMIResponseWriter(w http.ResponseWriter, resp gami.Message, err error, statusFromResponse bool,
	dataKey string) {
	if err != nil {
		glog.Errorln(err)
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
		return
	}

	if r, ok := resp["Response"]; ok && r == "Follows" {
		glog.Infoln("<<< RESPONSE...")
	} else {
		glog.Infoln("<<< RESPONSE", resp)
	}

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

func UnsignData(i interface{}, d model.SignedInputData) (err error) {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, []byte(getKey(conf.GetConf().Agencies[d.Country].Secret)))
	dataString, ok := signer.NewBase64Signer(h).Verify([]byte(d.Data))

	signatureData := strings.Split(d.Data, ".")
	if !ok || len(signatureData) < 2 {
		return errors.New("Bad signature")
	}
	err = json.Unmarshal(dataString, &i)
	dataString, signatureData = nil, nil
	return
}

func SendRequest(m map[string]string, url, method, secret, companyId string) (string, error) {
	glog.Infoln(fmt.Sprintf("Sending request to %v", url))
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
