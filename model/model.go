package model

import (
	"fmt"
	"github.com/zenazn/goji/param"
	"net/http"
)

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

type Queue struct {
	Queue          string `param:"queue"`
	Interface      string `param:"interface"`
	StateInterface string `param:"state_interface"`
}

type DbGetter struct {
	Family string `param:"family"`
	Key    string `param:"key"`
}

type PhoneCall struct {
	CallingPhone string `param:"calling_phone"`
	Country      string `param:"country"`
	Id           string `param:"id"`
	InnerNumber  string `param:"inner_number"`
	ReviewHref   string `param:"review_href"`
}

type SignedData struct {
	Data string
}

func GetStructFromParams(r *http.Request, s interface{}) (err error) {
	if (*r).Method == "POST" {
		r.ParseForm()
		err = param.Parse(r.Form, s)
	} else {
		err = param.Parse(r.URL.Query(), s)
	}
	return
}
