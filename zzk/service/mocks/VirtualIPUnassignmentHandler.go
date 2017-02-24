// Copyright 2017 The Serviced Authors.
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

package mocks

import "github.com/stretchr/testify/mock"

type VirtualIPUnassignmentHandler struct {
	mock.Mock
}

func (_m *VirtualIPUnassignmentHandler) UnassignAll(poolID, hostID string) error {
	ret := _m.Called(poolID, hostID)

	var r0 error
	if rf, ok := ret.Get(0).(func(string, string) error); ok {
		r0 = rf(poolID, hostID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
