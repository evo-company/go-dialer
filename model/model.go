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

func GetStructFromParams(r *http.Request, s interface{}) (err error) {
	r.ParseForm()
	err = param.Parse(r.Form, s)
	return
}
