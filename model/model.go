package model

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/zenazn/goji/param"
)

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

type PhoneCall struct {
	CallingPhone string `param:"calling_phone"`
	Country      string `param:"country" json:"country"`
	Id           string `param:"id"`
	InnerNumber  string `param:"inner_number" json:"inner_number"`
	ReviewHref   string `param:"review_href"`
}

type SignedInputData struct {
	Data    string `param:"data"`
	Country string `param:"country"`
}

type Cdr struct {
	Id string `param:"id"`
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
