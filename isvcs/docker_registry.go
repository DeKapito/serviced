// Copyright 2014 The Serviced Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package isvcs

import (
	"github.com/zenoss/glog"

	"fmt"
	"net/http"
	"time"
)

var dockerRegistry *IService

const registryPort = 5000

func init() {
	var err error

	defaultHealthCheck := healthCheckDefinition{
		healthCheck: registryHealthCheck,
		Interval:    DEFAULT_HEALTHCHECK_INTERVAL,
		Timeout:     DEFAULT_HEALTHCHECK_TIMEOUT,
	}
	healthChecks := map[string]healthCheckDefinition{
		DEFAULT_HEALTHCHECK_NAME: defaultHealthCheck,
	}

	dockerPortBinding := portBinding{
		HostIp:         "0.0.0.0",
		HostIpOverride: "", // docker registry should always be open
		HostPort:       registryPort,
	}
	command := `SETTINGS_FLAVOR=serviced exec /opt/registry/registry /opt/registry/registry-config.yml`
	dockerRegistry, err = NewIService(
		IServiceDefinition{
			Name:         "docker-registry",
			Repo:         IMAGE_REPO,
			Tag:          IMAGE_TAG,
			Command:      func() string { return command },
			PortBindings: []portBinding{dockerPortBinding},
			Volumes:      map[string]string{"registry": "/tmp/registry-dev"},
			HealthChecks: healthChecks,
		},
	)
	if err != nil {
		glog.Fatalf("Error initializing docker-registry container: %s", err)
	}
}

func registryHealthCheck(halt <-chan struct{}) error {
	url := fmt.Sprintf("http://localhost:%d/", registryPort)
	for {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			break
		} else {
			glog.V(1).Infof("Still trying to connect to docker registry at %s: %v", url, err)
		}

		select {
		case <-halt:
			glog.V(1).Infof("Quit healthcheck for docker registry at %s", url)
			return nil
		default:
			time.Sleep(time.Second)
		}
	}
	glog.V(1).Infof("docker registry running, browser at %s", url)
	return nil
}
