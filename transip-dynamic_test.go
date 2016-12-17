package main

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"testing"
)

func TestXML(t *testing.T) {
	data, _ := ioutil.ReadFile("test.xml")
	body := MyRespEnvelope{}

	err := xml.Unmarshal(data, &body)
	if err != nil {
		t.Error(err)
	}

	for _, i := range body.Body.GetInfoResponse.Return.DNSEntries.Info {
		fmt.Printf("%#v\n", i)
	}
	t.Fail()
}
