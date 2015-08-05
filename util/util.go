package util

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/parnurzeal/gorequest"
	"github.com/vmihailenco/signer"
	"github.com/warik/gami"

	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/model"
)

const (
	INCOMING_CALL = iota
	OUTGOING_CALL
	INNER_CALL
	UNKNOWN_CALL
	INCOMING_CALL_HIDDEN
	TIME_FORMAT = "2006-01-02 15:04:05"
)

var (
	PHONE_RE          *regexp.Regexp
	InnerPhoneNumbers InnerPhones
	callbackCdrCache  = NewSafeMap()
)

type InnerPhones struct {
	DuplicateNumbers model.Set
	NumbersMap       map[string]model.Set
	*sync.RWMutex
}

func LoadInnerNumbers(numbersChan chan<- []string) {
	for countryCode, settings := range conf.GetConf().Agencies {
		go func(countryCode string, settings model.CountrySettings) {
			url := conf.GetConf().GetApi(countryCode, "get_employees_inner_phone")
			payload, _ := json.Marshal(model.Dict{"CompanyId": settings.CompanyId})

			for i := 0; i < 5; i++ {
				numbers, err := SendRequest(payload, url, "GET", settings.Secret,
					settings.CompanyId)
				if err == nil {
					numbersChan <- []string{countryCode, numbers}
					return
				}
				glog.Errorln("Cannot load numbers", err)
				time.Sleep(5 * time.Second)
			}
		}(countryCode, settings)
	}
}

type SafeMap struct {
	Map map[string]interface{}
	*sync.RWMutex
}

func NewSafeMap() SafeMap {
	return SafeMap{map[string]interface{}{}, new(sync.RWMutex)}
}

func PowInt(a, b int) (res int) {
	res = 1
	for i := 0; i < a; i++ {
		res *= b
	}
	return
}

func getKey(secret string) []byte {
	sh := sha1.New()
	sh.Write([]byte("saltysigner" + secret))
	return sh.Sum(nil)
}

func signData(body []byte, secret string) (signedData string, err error) {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, getKey(secret))

	signedData = string(signer.NewBase64Signer(h).Sign(body))
	return
}

func ConvertTime(t string) string {
	tt, err := time.Parse(TIME_FORMAT, t)
	if err != nil {
		glog.Errorln(err)
		return ""
	}
	timeZoneShift := time.Duration(-conf.GetConf().TimeZone) * time.Hour
	return tt.Add(timeZoneShift).Format(TIME_FORMAT)
}

func ConvertWAV2MP3(dirName, wavFileName, mp3FileName string) error {
	lame := exec.Command("lame", "-h", "--add-id3v2", "-m", "m",
		fmt.Sprintf("%s/%s", dirName, wavFileName),
		fmt.Sprintf("%s_mp3/%s", dirName, mp3FileName))
	if out, err := lame.CombinedOutput(); err != nil {
		return errors.New(fmt.Sprintf("Error - %s | Output - %s", err.Error(), string(out)))
	}
	return nil
}

func GetPhoneCallFileName(dialerName, uniqueId, exten string) string {
	return fmt.Sprintf("%s-%s.%s", dialerName, uniqueId, exten)
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

func ShowCallingPopup(innerPhoneNumber, callingPhone, country string) (string, error) {
	settings := conf.GetConf().Agencies[country]
	payload, _ := json.Marshal(model.Dict{
		"inner_number":  innerPhoneNumber,
		"calling_phone": callingPhone,
		"CompanyId":     settings.CompanyId,
	})
	url := conf.GetConf().GetApi(country, "show_calling_popup_to_manager")
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	return resp, err
}

func ShowReviewPopup(reviewHref, innerNumber, country string) (string, error) {
	settings := conf.GetConf().Agencies[country]
	payload, _ := json.Marshal(model.Dict{
		"inner_number": innerNumber,
		"review_href":  reviewHref,
		"CompanyId":    settings.CompanyId,
	})
	url := conf.GetConf().GetApi(country, "show_calling_review_popup_to_manager")
	return SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
}

func GetCountryByPhones(innerPhoneNumber, opponentPhoneNumber string) (countryCode string) {
	InnerPhoneNumbers.RLock()
	defer InnerPhoneNumbers.RUnlock()
	//	If inner number is same for more than one portal it will be in separate structure
	//	And that means that we need to guess portal country by opponent number
	//	In other case just return country from map
	if _, ok := InnerPhoneNumbers.DuplicateNumbers[innerPhoneNumber]; ok {
		outerNumber := strings.TrimPrefix(opponentPhoneNumber, "+")
		if outerNumber[:3] == "380" || (outerNumber[:1] == "0" && len(outerNumber) == 10) {
			countryCode = "ua"
		} else if outerNumber[:2] == "77" {
			countryCode = "kz"
		} else if outerNumber[:2] == "80" || outerNumber[:3] == "375" {
			countryCode = "by"
		} else if strings.Contains("78", outerNumber[:1]) &&
			strings.Contains("3489", outerNumber[1:2]) {
			countryCode = "ru"
		}
	} else {
		for country, numbers := range InnerPhoneNumbers.NumbersMap {
			if _, ok := numbers[innerPhoneNumber]; ok {
				countryCode = country
				break
			}
		}
	}
	return
}

func GetCallBackPhoneDetails(channel, destination, destinationChannel string) (
	innerNum string, externNum string, callType int) {
	callbackCdrCache.Lock()
	defer callbackCdrCache.Unlock()

	innerNum, externNum, callType = "", "", -1
	channelSplit := strings.Split(channel, ";")
	channelKey, channelNum := channelSplit[0], channelSplit[1]
	if _, ok := callbackCdrCache.Map[channelKey]; !ok {
		if channelNum == "1" {
			callbackCdrCache.Map[channelKey] = destination
		} else {
			innerNumArr := PHONE_RE.FindStringSubmatch(destinationChannel)
			if innerNumArr != nil {
				callbackCdrCache.Map[channelKey] = innerNumArr[1]
			}
		}
		return
	}

	firstNum := callbackCdrCache.Map[channelKey].(string)
	if channelNum == "1" {
		innerNum, externNum, callType = firstNum, destination, OUTGOING_CALL
	} else {
		innerNumArr := PHONE_RE.FindStringSubmatch(destinationChannel)
		if innerNumArr != nil {
			innerNum, externNum, callType = innerNumArr[1], firstNum, OUTGOING_CALL
		}
	}
	return
}

func GetPhoneDetails(channel, destChannel, source, destination, callerId string) (string,
	string, int) {
	in := PHONE_RE.FindStringSubmatch(channel)
	out := PHONE_RE.FindStringSubmatch(destChannel)
	if in != nil && out != nil {
		// If both channels contain inner numbers:
		// - most likely that this is incoming call passed through queue
		// - or hidden incoming call
		// - or inner call (should be passed)
		if source == "" && callerId == "" {
			return out[1], "xxxx", INCOMING_CALL_HIDDEN
		} else if len(source) > 4 {
			return out[1], source, INCOMING_CALL
		} else {
			return "", "", INNER_CALL
		}
	}
	if in != nil && len(destination) > 4 {
		// If incoming channel contains inner number - just outgoing call
		return in[1], destination, OUTGOING_CALL
	}
	if out != nil {
		// If outcome channel contains inner number:
		// - most likely that incoming
		// - or hidden (should be passed)
		// - or incoming into queue (should be passed)
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

func IsNumbersValid(innerPhoneNumber, opponentPhoneNumber, countryCode string) bool {
	if len(innerPhoneNumber) < 3 && len(innerPhoneNumber) > 5 {
		return false
	}
	if countryCode == "by" && len(opponentPhoneNumber) < 9 {
		return false
	}
	return len(opponentPhoneNumber) >= 7
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

func SendRequest(payload []byte, url, method, secret, companyId string) (string, error) {
	glog.Infoln(fmt.Sprintf("Sending request to %v", url))
	signedData, err := signData(payload, secret)
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

func init() {
	PHONE_RE, _ = regexp.Compile("^\\w+/(\\d{2,4}|\\d{4}\\w{2})\\D*-.+$")
	InnerPhoneNumbers = InnerPhones{model.Set{}, map[string]model.Set{}, new(sync.RWMutex)}
}
