// Copyright 2016 The Serviced Authors.
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

// Package stats collects serviced metrics and posts them to the TSDB.

package stats

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/control-center/go-procfs/linux"
	coordclient "github.com/control-center/serviced/coordinator/client"
	"github.com/control-center/serviced/dfs/docker"
	"github.com/control-center/serviced/utils"
	zkservice "github.com/control-center/serviced/zzk/service"
	"github.com/rcrowley/go-metrics"
	"github.com/zenoss/glog"

	"time"
)

// ServicedStatsReporter collects and posts serviced/docker stats to the TSDB.
type ServicedStatsReporter struct {
	statsReporter
	sync.Mutex
	hostID              string
	hostRegistry        metrics.Registry
	previousStats       map[string]map[string]uint64 //holds some of the stats gathered in the previous sample, currently used for computing CPU %
	conn                coordclient.Connection
	containerRegistries map[registryKey]metrics.Registry
	docker              docker.Docker
}

type registryKey struct {
	serviceID  string
	instanceID int
}

// NewServicedStatsReporter creates a new ServicedStatsReporter and kicks off the reporting goroutine.
func NewServicedStatsReporter(destination string, interval time.Duration, conn coordclient.Connection, dockerClient docker.Docker) (*ServicedStatsReporter, error) {
	hostID, err := utils.HostID()
	if err != nil {
		glog.Errorf("Could not determine host ID.")
		return nil, err
	}
	if conn == nil {
		glog.Errorf("conn can not be nil")
		return nil, fmt.Errorf("conn can not be nil")
	}
	ssr := ServicedStatsReporter{
		statsReporter: statsReporter{
			destination:  destination,
			closeChannel: make(chan struct{}),
		},
		hostID:              hostID,
		previousStats:       make(map[string]map[string]uint64),
		conn:                conn,
		containerRegistries: make(map[registryKey]metrics.Registry),
		docker:              dockerClient,
	}

	ssr.hostRegistry = metrics.NewRegistry()
	ssr.statsReporter.updateStatsFunc = ssr.updateStats
	ssr.statsReporter.gatherStatsFunc = ssr.gatherStats
	go ssr.report(interval)
	return &ssr, nil
}

// getOrCreateContainerRegistry returns a registry for a given service id or creates it
// if it doesn't exist.
func (sr *ServicedStatsReporter) getOrCreateContainerRegistry(serviceID string, instanceID int) metrics.Registry {
	key := registryKey{serviceID, instanceID}
	if registry, ok := sr.containerRegistries[key]; ok {
		return registry
	}
	sr.Lock()
	defer sr.Unlock()
	sr.containerRegistries[key] = metrics.NewRegistry()
	return sr.containerRegistries[key]
}

func (sr *ServicedStatsReporter) removeStaleRegistries(states []zkservice.State) {
	// First build a list of what's actually running
	keys := make(map[string][]int)
	containers := make(map[string]bool)
	for _, rs := range states {
		containers[rs.ContainerID] = true
		if instances, ok := keys[rs.ServiceID]; !ok {
			instances = []int{rs.InstanceID}
			keys[rs.ServiceID] = instances
		} else {
			keys[rs.ServiceID] = append(keys[rs.ServiceID], rs.InstanceID)
		}
	}
	// Now remove any keys that are in the registry but are no longer running
	// on this host
	sr.Lock()
	defer sr.Unlock()
	for key, _ := range sr.containerRegistries {
		if instances, ok := keys[key.serviceID]; !ok {
			delete(sr.containerRegistries, key)
		} else {
			var seen bool
			for _, instanceid := range instances {
				if instanceid == key.instanceID {
					seen = true
					break
				}
			}
			if !seen {
				delete(sr.containerRegistries, key)
			}
		}
	}

	// Now remove stale entries from our list of previous stats
	for key, _ := range sr.previousStats {
		if _, ok := containers[key]; !ok {
			delete(sr.previousStats, key)
		}
	}
}

// Fills out the metric consumer format.
func (sr *ServicedStatsReporter) gatherStats(t time.Time) []Sample {
	stats := []Sample{}
	// Handle the host metrics.
	reg, _ := sr.hostRegistry.(*metrics.StandardRegistry)
	reg.Each(func(name string, i interface{}) {
		if metric, ok := i.(metrics.Gauge); ok {
			tagmap := make(map[string]string)
			tagmap["controlplane_host_id"] = sr.hostID
			stats = append(stats, Sample{name, strconv.FormatInt(metric.Value(), 10), t.Unix(), tagmap})
		}
		if metricf64, ok := i.(metrics.GaugeFloat64); ok {
			tagmap := make(map[string]string)
			tagmap["controlplane_host_id"] = sr.hostID
			stats = append(stats, Sample{name, strconv.FormatFloat(metricf64.Value(), 'f', -1, 32), t.Unix(), tagmap})
		}
	})
	// Handle each container's metrics.
	for key, registry := range sr.containerRegistries {
		reg, _ := registry.(*metrics.StandardRegistry)
		reg.Each(func(name string, i interface{}) {
			tagmap := make(map[string]string)
			tagmap["controlplane_service_id"] = key.serviceID
			tagmap["controlplane_instance_id"] = strconv.FormatInt(int64(key.instanceID), 10)
			tagmap["controlplane_host_id"] = sr.hostID
			if metric, ok := i.(metrics.Gauge); ok {
				stats = append(stats, Sample{name, strconv.FormatInt(metric.Value(), 10), t.Unix(), tagmap})
			} else if metricf64, ok := i.(metrics.GaugeFloat64); ok {
				stats = append(stats, Sample{name, strconv.FormatFloat(metricf64.Value(), 'f', -1, 32), t.Unix(), tagmap})
			}
		})
	}
	return stats
}

// Updates the default registry.
func (sr *ServicedStatsReporter) updateStats() {
	// Stats for host.
	sr.updateHostStats()
	// Stats for the containers.
	states, err := zkservice.GetHostStates(sr.conn, "", sr.hostID)
	if err != nil {
		glog.Errorf("updateStats: zkservice.GetHostStates (conn: %+v hostID: %v) failed: %v", sr.conn, sr.hostID, err)
	}

	for _, rs := range states {
		if rs.ContainerID != "" {

			containerRegistry := sr.getOrCreateContainerRegistry(rs.ServiceID, rs.InstanceID)
			stats, err := sr.docker.GetContainerStats(rs.ContainerID, 30*time.Second)
			if err != nil || stats == nil { //stats may be nil if service is shutting down
				glog.Warningf("Couldn't get stats for service %s instance %d: %v", rs.ServiceID, rs.InstanceID, err)
				continue
			}

			// Check to see if we have the previous stats for this running instance
			usePreviousStats := true
			key := rs.ContainerID
			if _, found := sr.previousStats[key]; !found {
				sr.previousStats[key] = make(map[string]uint64)
				usePreviousStats = false
			}

			// CPU Stats
			// TODO: Consolidate this into a single object that both ISVCS and non-ISVCS can use
			var (
				kernelCPUPercent float64
				userCPUPercent   float64
				totalCPUChange   uint64
			)

			kernelCPU := stats.CPUStats.CPUUsage.UsageInKernelmode
			userCPU := stats.CPUStats.CPUUsage.UsageInUsermode
			totalCPU := stats.CPUStats.SystemCPUUsage

			// Total CPU Cycles
			previousTotalCPU, found := sr.previousStats[key]["totalCPU"]
			if found {
				if totalCPU <= previousTotalCPU {
					glog.Warningf("Change in total CPU usage was nonpositive, skipping CPU stats update.")
					usePreviousStats = false
				} else {
					totalCPUChange = totalCPU - previousTotalCPU
				}
			} else {
				usePreviousStats = false
			}
			sr.previousStats[key]["totalCPU"] = totalCPU

			// CPU Cycles in Kernel mode
			if previousKernelCPU, found := sr.previousStats[key]["kernelCPU"]; found && usePreviousStats {
				kernelCPUChange := kernelCPU - previousKernelCPU
				kernelCPUPercent = (float64(kernelCPUChange) / float64(totalCPUChange)) * float64(len(stats.CPUStats.CPUUsage.PercpuUsage)) * 100.0
			} else {
				usePreviousStats = false
			}
			sr.previousStats[key]["kernelCPU"] = kernelCPU

			// CPU Cycles in User mode
			if previousUserCPU, found := sr.previousStats[key]["userCPU"]; found && usePreviousStats {
				userCPUChange := userCPU - previousUserCPU
				userCPUPercent = (float64(userCPUChange) / float64(totalCPUChange)) * float64(len(stats.CPUStats.CPUUsage.PercpuUsage)) * 100.0
			} else {
				usePreviousStats = false
			}
			sr.previousStats[key]["userCPU"] = userCPU

			// Update CPU metrics
			if usePreviousStats {
				metrics.GetOrRegisterGaugeFloat64("docker.usageinkernelmode", containerRegistry).Update(kernelCPUPercent)
				metrics.GetOrRegisterGaugeFloat64("docker.usageinusermode", containerRegistry).Update(userCPUPercent)
			} else {
				glog.V(4).Infof("Skipping CPU stats for %s (%d) , no previous values to compare to", rs.ServiceID, rs.InstanceID)
			}

			// Memory Stats
			pgFault := int64(stats.MemoryStats.Stats.Pgfault)
			totalRSS := int64(stats.MemoryStats.Stats.TotalRss)
			cache := int64(stats.MemoryStats.Stats.Cache)
			if pgFault < 0 || totalRSS < 0 || cache < 0 {
				glog.Warningf("Memory metric value for service %s instance %d too big for int64", rs.ServiceID, rs.InstanceID)
			}
			metrics.GetOrRegisterGauge("cgroup.memory.pgmajfault", containerRegistry).Update(pgFault)
			metrics.GetOrRegisterGauge("cgroup.memory.totalrss", containerRegistry).Update(totalRSS)
			metrics.GetOrRegisterGauge("cgroup.memory.cache", containerRegistry).Update(cache)

		} else {
			glog.V(4).Infof("Skipping stats update for %s (%d), no container ID exists yet", rs.ServiceID, rs.InstanceID)
		}
	}
	// Clean out old container registries
	sr.removeStaleRegistries(states)
}

func (sr *ServicedStatsReporter) updateHostStats() {

	loadavg, err := linux.ReadLoadavg()
	if err != nil {
		glog.Errorf("could not read loadavg: %s", err)
		return
	}
	metrics.GetOrRegisterGaugeFloat64("load.avg1m", sr.hostRegistry).Update(float64(loadavg.Avg1m))
	metrics.GetOrRegisterGaugeFloat64("load.avg5m", sr.hostRegistry).Update(float64(loadavg.Avg5m))
	metrics.GetOrRegisterGaugeFloat64("load.avg10m", sr.hostRegistry).Update(float64(loadavg.Avg10m))
	metrics.GetOrRegisterGauge("load.runningprocesses", sr.hostRegistry).Update(int64(loadavg.RunningProcesses))
	metrics.GetOrRegisterGauge("load.totalprocesses", sr.hostRegistry).Update(int64(loadavg.TotalProcesses))

	stat, err := linux.ReadStat()
	if err != nil {
		glog.Errorf("could not read stat: %s", err)
		return
	}
	metrics.GetOrRegisterGauge("cpu.user", sr.hostRegistry).Update(int64(stat.Cpu.User()))
	metrics.GetOrRegisterGauge("cpu.nice", sr.hostRegistry).Update(int64(stat.Cpu.Nice()))
	metrics.GetOrRegisterGauge("cpu.system", sr.hostRegistry).Update(int64(stat.Cpu.System()))
	metrics.GetOrRegisterGauge("cpu.idle", sr.hostRegistry).Update(int64(stat.Cpu.Idle()))
	metrics.GetOrRegisterGauge("cpu.iowait", sr.hostRegistry).Update(int64(stat.Cpu.Iowait()))
	metrics.GetOrRegisterGauge("cpu.irq", sr.hostRegistry).Update(int64(stat.Cpu.Irq()))
	metrics.GetOrRegisterGauge("cpu.softirq", sr.hostRegistry).Update(int64(stat.Cpu.Softirq()))
	var steal int64
	if stat.Cpu.StealSupported() {
		steal = int64(stat.Cpu.Steal())
	}
	metrics.GetOrRegisterGauge("cpu.steal", sr.hostRegistry).Update(steal)

	meminfo, err := linux.ReadMeminfo()
	if err != nil {
		glog.Errorf("could not read meminfo: %s", err)
		return
	}
	metrics.GetOrRegisterGauge("memory.total", sr.hostRegistry).Update(int64(meminfo.MemTotal))
	metrics.GetOrRegisterGauge("memory.free", sr.hostRegistry).Update(int64(meminfo.MemFree))
	metrics.GetOrRegisterGauge("memory.buffers", sr.hostRegistry).Update(int64(meminfo.Buffers))
	metrics.GetOrRegisterGauge("memory.cached", sr.hostRegistry).Update(int64(meminfo.Cached))
	actualFree := int64(meminfo.MemFree) + int64(meminfo.Buffers) + int64(meminfo.Cached)
	metrics.GetOrRegisterGauge("memory.actualfree", sr.hostRegistry).Update(actualFree)
	metrics.GetOrRegisterGauge("memory.actualused", sr.hostRegistry).Update(int64(meminfo.MemTotal) - actualFree)
	metrics.GetOrRegisterGauge("swap.total", sr.hostRegistry).Update(int64(meminfo.SwapTotal))
	metrics.GetOrRegisterGauge("swap.free", sr.hostRegistry).Update(int64(meminfo.SwapFree))

	vmstat, err := linux.ReadVmstat()
	if err != nil {
		glog.Errorf("could not read vmstat: %s", err)
		return
	}
	metrics.GetOrRegisterGauge("vmstat.pgfault", sr.hostRegistry).Update(int64(vmstat.Pgfault))
	metrics.GetOrRegisterGauge("vmstat.pgmajfault", sr.hostRegistry).Update(int64(vmstat.Pgmajfault))
	metrics.GetOrRegisterGauge("vmstat.pgpgout", sr.hostRegistry).Update(int64(vmstat.Pgpgout) * 1024)
	metrics.GetOrRegisterGauge("vmstat.pgpgin", sr.hostRegistry).Update(int64(vmstat.Pgpgin) * 1024)
	metrics.GetOrRegisterGauge("vmstat.pswpout", sr.hostRegistry).Update(int64(vmstat.Pswpout) * 1024)
	metrics.GetOrRegisterGauge("vmstat.pswpin", sr.hostRegistry).Update(int64(vmstat.Pswpin) * 1024)

	if openFileDescriptorCount, err := GetOpenFileDescriptorCount(); err != nil {
		glog.Warningf("Couldn't get open file descriptor count", err)
	} else {
		metrics.GetOrRegisterGauge("Serviced.OpenFileDescriptors", sr.hostRegistry).Update(openFileDescriptorCount)
	}
}
