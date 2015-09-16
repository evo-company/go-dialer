package model

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/goji/param"
)

type Set map[string]struct{}
type Dict map[string]string
type Response map[string]interface{}

func (r Response) String() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

type CountrySettings struct {
	CompanyId string `json:"companyId"`
	Secret    string `json:"secret"`
}

type Call struct {
	Inline string `param:"inline"`
	Exten  string `param:"exten"`
}

func (c Call) GetChannel() string {
	return fmt.Sprintf("SIP/%v", c.Inline)
}

func (c Call) GetCallerID() string {
	return fmt.Sprintf("call_from_CRM <%v>", c.Inline)
}

type CallInQueue struct {
	PhoneNumber string `param:"phone_number" json:"phone_number"`
	Country     string `param:"country"`
}

type QueueContainer struct {
	InnerNumber string `param:"inner_number" json:"inner_number"`
	Queue       string `param:"queue" json:"queue"`
}

type DialerStats struct {
	Name    string
	DBCount int
}

type PhoneCall struct {
	Country      string `param:"country" json:"country"`
	CallingPhone string `param:"calling_phone"`
	Id           string `param:"id"`
	InnerNumber  string `param:"inner_number" json:"inner_number"`
	ReviewHref   string `param:"review_href"`
}

type SignedInputData struct {
	Country string `param:"country"`
	Data    string `param:"data"`
}

type Cdr struct {
	Id       int    `param:"id"`
	UniqueID string `param:"unique_id"`
}

type SignedData struct {
	Data      string
	CompanyId string
}

type DummyStruct struct {
	Dummy string
}

func GetStructFromParams(r *http.Request, s interface{}) (err error) {
	if (*r).Method == "POST" {
		err = r.ParseForm()
		err = param.Parse(r.Form, s)
	} else {
		err = param.Parse(r.URL.Query(), s)
	}
	return
}
