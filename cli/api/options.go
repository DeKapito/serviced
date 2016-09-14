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

package api

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/isvcs"
	"github.com/control-center/serviced/node"
	"github.com/control-center/serviced/rpc/rpcutils"
	"github.com/control-center/serviced/utils"
	"github.com/control-center/serviced/validation"
	"github.com/control-center/serviced/volume"
	"github.com/zenoss/glog"
	"github.com/zenoss/logri"
)

const (
	DefaultRPCPort       = 4979
	outboundIPRetryDelay = 1
	outboundIPMaxWait    = 90
)

var options Options

// Options are the server options
type Options struct {
	Endpoint                   string // the endpoint address to make RPC requests to
	UIPort                     string
	NFSClient                  string
	RPCPort                    string
	Listen                     string
	OutboundIP                 string // outbound ip to listen on
	Master                     bool
	DockerDNS                  []string
	Agent                      bool
	MuxPort                    int
	MuxDisableTLS              bool // Whether TLS should be disabled for the mux
	KeyPEMFile                 string
	CertPEMFile                string
	VolumesPath                string
	IsvcsPath                  string
	BackupsPath                string
	ResourcePath               string
	Zookeepers                 []string
	ReportStats                bool
	HostStats                  string
	StatsPeriod                int
	SvcStatsCacheTimeout       int
	MCUsername                 string
	MCPasswd                   string
	Mount                      []string
	ResourcePeriod             int
	FSType                     volume.DriverType
	ESStartupTimeout           int
	HostAliases                []string
	Verbosity                  int
	StaticIPs                  []string
	DockerRegistry             string
	CPUProfile                 string // write cpu profile to file
	MaxContainerAge            int    // max container age in seconds
	MaxDFSTimeout              int    // max timeout for snapshot
	VirtualAddressSubnet       string
	MasterPoolID               string
	LogstashES                 string //logstash elasticsearch host:port
	LogstashMaxDays            int    // Days to keep logstash indices
	LogstashMaxSize            int    // Max size of logstash data
	LogstashCycleTime          int    // Logstash purging cycle time in hours
	LogstashURL                string
	DebugPort                  int    // Port to listen for profile clients
	AdminGroup                 string // user group that can log in to control center
	MaxRPCClients              int    // the max number of rpc clients to an endpoint
	MUXTLSCiphers              []string          // List of tls ciphers supported for mux
	MUXTLSMinVersion           string            // Minimum TLS version supported for mux
	RPCDialTimeout             int
	RPCCertVerify              string            //  server certificate verify for rpc connections, string val of bool
	RPCDisableTLS              string            //  Disable TLS for RPC connections, string val of bool
	RPCTLSCiphers              []string          // List of tls ciphers supported for rpc
	RPCTLSMinVersion           string            // Minimum TLS version supported for rpc
	SnapshotTTL                int               // hours to keep snapshots around, zero for infinity
	StorageArgs                []string          // command-line arguments for storage options
	StorageOptions             map[string]string // environment arguments for storage options
	ControllerBinary           string            // Path to the container controller binary
	StartISVCS                 []string          // ISVCS to start when running as an agent
	IsvcsZKID                  int               // Zookeeper server id when running as a quorum
	IsvcsZKQuorum              []string          // Members of the zookeeper quorum
	TLSCiphers                 []string          // List of tls ciphers supported for http
	TLSMinVersion              string            // Minimum TLS version supported for http
	DockerLogDriver            string            // Which log driver to use with containers
	DockerLogConfigList        []string          // List of comma-separated key=value options for docker logging
	AllowLoopBack              string            // Allow loop back devices for DM storage, string val of bool
	UIPollFrequency            int               // frequency in seconds that UI should poll for service changes
	StorageStatsUpdateInterval int               // frequency in seconds that low-level devicemapper storage stats should be refreshed
	SnapshotSpacePercent       int               // Percent of tenant volume size that is assumed to be needed to create a snapshot
	ZKSessionTimeout           int               // The session timeout of a zookeeper client connection.
}

// LoadOptions overwrites the existing server options
func LoadOptions(ops Options) {
	options = ops

	// Set verbosity
	glog.SetVerbosity(options.Verbosity)
	level := logrus.InfoLevel + logrus.Level(options.Verbosity)
	logri.SetLevel(level)

	// Check option boundaries
	if options.ESStartupTimeout < minTimeout {
		log.WithFields(logrus.Fields{
			"mintimeout": minTimeout,
		}).Debug("Overriding Elastic startup timeout")
		options.ESStartupTimeout = minTimeout
	}
}

// Validate options which are common to all CLI commands
func ValidateCommonOptions(opts Options) error {
	var err error

	rpcutils.RPCCertVerify, err = strconv.ParseBool(opts.RPCCertVerify)
	if err != nil {
		return fmt.Errorf("error parsing rpc-cert-verify value %v", err)
	}
	rpcutils.RPCDisableTLS, err = strconv.ParseBool(opts.RPCDisableTLS)
	if err != nil {
		return fmt.Errorf("error parsing rpc-disable-tls value %v", err)
	}

	if err := validation.ValidUIAddress(opts.UIPort); err != nil {
		return fmt.Errorf("error validating UI port: %s", err)
	}

	// TODO: move this to ValidateServerOptions if this is really only used by master/agent, and not cli
	if err := validation.IsSubnetCIDR(opts.VirtualAddressSubnet); err != nil {
		return fmt.Errorf("error validating virtual-address-subnet: %s", err)
	}

	return nil
}

// Validate options which are specific to running a master and/or agent
func ValidateServerOptions() error {
	if !options.Master && !options.Agent {
		return fmt.Errorf("serviced cannot be started: no mode (master or agent) was specified")
	} else if err := validateStorageArgs(); err != nil {
		return err
	}

	// Make sure we have an endpoint to work with
	if len(options.Endpoint) == 0 {
		if options.Master {
			outboundIP, err := getOutboundIP()
			if err != nil {
				log.WithError(err).Fatal("Unable to determine outbound IP")
			}
			options.Endpoint = fmt.Sprintf("%s:%s", outboundIP, options.RPCPort)
		} else {
			return fmt.Errorf("No endpoint to master has been configured")
		}
	}

	if options.Master {
		log.WithFields(logrus.Fields{
			"poolid": options.MasterPoolID,
		}).Debug("Using configured default pool ID")
	}
	return nil
}

// GetOptionsRPCEndpoint returns the serviced RPC endpoint from options
func GetOptionsRPCEndpoint() string {
	return options.Endpoint
}

// GetOptionsRPCPort returns the serviced RPC port from options
func GetOptionsRPCPort() string {
	return options.RPCPort
}

// GetOptionsMaster returns the master mode setting from options
func GetOptionsMaster() bool {
	return options.Master
}

// GetOptionsAgent returns the agent mode setting from options
func GetOptionsAgent() bool {
	return options.Agent
}

// GetOptionsMasterPoolID returns the master pool ID from options
func GetOptionsMasterPoolID() string {
	return options.MasterPoolID
}

// GetOptionsMaxRPCClients returns the max RPC clients setting from options
func GetOptionsMaxRPCClients() int {
	return options.MaxRPCClients
}

func GetDefaultOptions(config utils.ConfigReader) Options {
	masterIP := config.StringVal("MASTER_IP", "127.0.0.1")

	options := Options{
		UIPort:                     service.ScrubPortString(config.StringVal("UI_PORT", ":443")),
		NFSClient:                  config.StringVal("NFS_CLIENT", "1"),
		RPCPort:                    config.StringVal("RPC_PORT", fmt.Sprintf("%d", DefaultRPCPort)),
		OutboundIP:                 config.StringVal("OUTBOUND_IP", ""),
		DockerDNS:                  config.StringSlice("DOCKER_DNS", []string{}),
		Master:                     config.BoolVal("MASTER", false),
		Agent:                      config.BoolVal("AGENT", false),
		MuxPort:                    config.IntVal("MUX_PORT", 22250),
		MuxDisableTLS:              config.BoolVal("MUX_DISABLE_TLS", false),
		KeyPEMFile:                 config.StringVal("KEY_FILE", ""),
		CertPEMFile:                config.StringVal("CERT_FILE", ""),
		Zookeepers:                 config.StringSlice("ZK", []string{}),
		HostStats:                  config.StringVal("STATS_PORT", fmt.Sprintf("%s:8443", masterIP)),
		StatsPeriod:                config.IntVal("STATS_PERIOD", 10),
		SvcStatsCacheTimeout:       config.IntVal("SVCSTATS_CACHE_TIMEOUT", 5),
		MCUsername:                 "scott",
		MCPasswd:                   "tiger",
		FSType:                     volume.DriverType(config.StringVal("FS_TYPE", "devicemapper")),
		ESStartupTimeout:           getDefaultESStartupTimeout(config.IntVal("ES_STARTUP_TIMEOUT", isvcs.DEFAULT_ES_STARTUP_TIMEOUT_SECONDS)),
		HostAliases:                config.StringSlice("VHOST_ALIASES", []string{}),
		Verbosity:                  config.IntVal("LOG_LEVEL", 0),
		StaticIPs:                  config.StringSlice("STATIC_IPS", []string{}),
		DockerRegistry:             config.StringVal("DOCKER_REGISTRY", "localhost:5000"),
		MaxContainerAge:            config.IntVal("MAX_CONTAINER_AGE", 60*60*24),
		MaxDFSTimeout:              config.IntVal("MAX_DFS_TIMEOUT", 60*5),
		VirtualAddressSubnet:       config.StringVal("VIRTUAL_ADDRESS_SUBNET", "10.3.0.0/16"),
		MasterPoolID:               config.StringVal("MASTER_POOLID", "default"),
		LogstashES:                 config.StringVal("LOGSTASH_ES", fmt.Sprintf("%s:9100", masterIP)),
		LogstashURL:                config.StringVal("LOG_ADDRESS", fmt.Sprintf("%s:5042", masterIP)),
		LogstashMaxDays:            config.IntVal("LOGSTASH_MAX_DAYS", 14),
		LogstashMaxSize:            config.IntVal("LOGSTASH_MAX_SIZE", 10),
		LogstashCycleTime:          config.IntVal("LOGSTASH_CYCLE_TIME", 6),
		DebugPort:                  config.IntVal("DEBUG_PORT", 6006),
		AdminGroup:                 config.StringVal("ADMIN_GROUP", getDefaultAdminGroup()),
		MaxRPCClients:              config.IntVal("MAX_RPC_CLIENTS", 3),
		MUXTLSCiphers:              config.StringSlice("MUX_TLS_CIPHERS", utils.GetDefaultCiphers("mux")),
		MUXTLSMinVersion:           config.StringVal("MUX_TLS_MIN_VERSION", utils.DefaultTLSMinVersion),
		RPCDialTimeout:             config.IntVal("RPC_DIAL_TIMEOUT", 30),
		RPCCertVerify:              strconv.FormatBool(config.BoolVal("RPC_CERT_VERIFY", false)),
		RPCDisableTLS:              strconv.FormatBool(config.BoolVal("RPC_DISABLE_TLS", false)),
		RPCTLSCiphers:              config.StringSlice("RPC_TLS_CIPHERS", utils.GetDefaultCiphers("rpc")),
		RPCTLSMinVersion:           config.StringVal("RPC_TLS_MIN_VERSION", utils.DefaultTLSMinVersion),
		SnapshotTTL:                config.IntVal("SNAPSHOT_TTL", 12),
		StartISVCS:                 config.StringSlice("ISVCS_START", []string{}),
		IsvcsZKID:                  config.IntVal("ISVCS_ZOOKEEPER_ID", 0),
		IsvcsZKQuorum:              config.StringSlice("ISVCS_ZOOKEEPER_QUORUM", []string{}),
		TLSCiphers:                 config.StringSlice("TLS_CIPHERS", utils.GetDefaultCiphers("http")),
		TLSMinVersion:              config.StringVal("TLS_MIN_VERSION", utils.DefaultTLSMinVersion),
		DockerLogDriver:            config.StringVal("DOCKER_LOG_DRIVER", "json-file"),
		DockerLogConfigList:        config.StringSlice("DOCKER_LOG_CONFIG", []string{"max-file=5", "max-size=10m"}),
		AllowLoopBack:              strconv.FormatBool(config.BoolVal("ALLOW_LOOP_BACK", false)),
		UIPollFrequency:            config.IntVal("UI_POLL_FREQUENCY", 3),
		StorageStatsUpdateInterval: config.IntVal("STORAGE_STATS_UPDATE_INTERVAL", 300),
		SnapshotSpacePercent:       config.IntVal("SNAPSHOT_USE_PERCENT", 20),
		ZKSessionTimeout:           config.IntVal("ZK_SESSION_TIMEOUT", 15),
	}

	options.Endpoint = config.StringVal("ENDPOINT", "")

	// Set the path to the controller binary
	dir, _, err := node.ExecPath()
	if err != nil {
		log.Warn("Unable to find path to serviced binary; assuming /opt/serviced/bin")
		dir = "/opt/serviced/bin"
	}
	defaultControllerBinary := filepath.Join(dir, "serviced-controller")
	options.ControllerBinary = config.StringVal("CONTROLLER_BINARY", defaultControllerBinary)

	homepath := config.StringVal("HOME", "")
	varpath := getDefaultVarPath(homepath)

	options.IsvcsPath = config.StringVal("ISVCS_PATH", filepath.Join(varpath, "isvcs"))
	options.VolumesPath = config.StringVal("VOLUMES_PATH", filepath.Join(varpath, "volumes"))
	options.BackupsPath = config.StringVal("BACKUPS_PATH", filepath.Join(varpath, "backups"))
	options.StorageArgs = getDefaultStorageOptions(options.FSType, config)

	return options
}

func getDefaultVarPath(home string) string {
	if home == "" {
		if user, err := user.Current(); err != nil {
			home = filepath.Join(os.TempDir(), "serviced")
		} else {
			home = filepath.Join(os.TempDir(), "serviced-"+user.Username)
		}
	}

	return filepath.Join(home, "var")
}

func getDefaultESStartupTimeout(timeout int) int {
	minTimeout := isvcs.MIN_ES_STARTUP_TIMEOUT_SECONDS
	if timeout < minTimeout {
		timeout = minTimeout
	}
	return timeout
}

func getDefaultAdminGroup() string {
	if utils.Platform == utils.Rhel {
		return "wheel"
	}
	return "sudo"
}

// getOutboundIP queries the network configuration for an IP address suitable for reaching the outside world.
// Will retry for a while if a path to the outside world is not yet available.
func getOutboundIP() (string, error) {
	var outboundIP string
	var err error
	timeout := time.After(outboundIPMaxWait * time.Second)
	for {
		if outboundIP, err = utils.GetIPAddress(); err == nil {
			// Success
			return outboundIP, nil
		}
		select {
		case <-timeout:
			// Give up
			return "", fmt.Errorf("Gave up waiting for network (to determine our outbound IP address)")
		default:
			// Retry
			log.Debug("Waiting for network initialization")
			time.Sleep(outboundIPRetryDelay * time.Second)
		}
	}
}
