// Copyright 2015 The Serviced Authors.
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

package master

import (
	"errors"

	"github.com/control-center/serviced/volume"
)

// GetVolumeStatus gets the volume status
func (s *Server) GetVolumeStatus(empty struct{}, reply *volume.Statuses) error {
	response := volume.GetStatus()
	if response == nil {
		return errors.New("volume_server.go GetStatus failed")
	}
	*reply = *response
	return nil
}
