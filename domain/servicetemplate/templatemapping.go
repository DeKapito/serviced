// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by a
// license that can be found in the LICENSE file.

package servicetemplate

import (
	"github.com/zenoss/glog"
	"github.com/zenoss/serviced/datastore/elastic"
)

var (
	mappingString = `
{
  "servicetemplatewrapper" : {
    "properties" : {
      "Id" : {
        "type"  : "string",
        "index" : "not_analyzed"
      },
      "Name" : {
        "type"  : "string",
        "index" : "not_analyzed"
      },
      "Description" : {
        "type"  : "string",
        "index" : "not_analyzed"
      },
      "ApiVersion" : {
        "type"  : "long",
        "index" : "not_analyzed"
      },
      "TemplateVersion" : {
        "type"  : "long",
        "index" : "not_analyzed"
      },
      "Data" : {
        "type"  : "string",
        "index" : "not_analyzed"
      }
    }
  }
}
`
	//MAPPING is the elastic mapping for a service template
	MAPPING, mappingError = elastic.NewMapping(mappingString)
)

func init() {
	if mappingError != nil {
		glog.Fatalf("error creating host mapping: %v", mappingError)
	}
}
