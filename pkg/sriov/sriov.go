/*
Copyright 2017 Mirantis

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sriov

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/glog"
	libvirtxml "github.com/libvirt/libvirt-go-xml"
)

const (
	AllocatedDevicesStorePath = "/var/lib/virtlet/used_vfs.txt"
)

var (
	fileAccessMutex *sync.Mutex
)

func init() {
	fileAccessMutex = &sync.Mutex{}
}

// VerifyMasterDevices checks if there are network interfaces with names
// passed in devNames slice and do they have enabled virtual functions
func VerifyMasterDevices(devNames []string) error {
	for _, devName := range devNames {
		glog.Infof("Checking %q for available VFs", devName)
		numvfsPath := "/sys/class/net/" + devName + "/device/sriov_numvfs"
		if data, err := ioutil.ReadFile(numvfsPath); err != nil {
			return fmt.Errorf("cannot read from %q path: %v", numvfsPath, err)
		} else if value, err := strconv.Atoi(strings.TrimSpace(string(data))); err != nil {
			return fmt.Errorf("cannot interpret %q as integer number: %v", data, err)
		} else if value == 0 {
			return fmt.Errorf("device %q have 0 configured VFs", devName)
		}
	}

	return nil
}

// AllocateDeviceOn looks on master devices searching for first available
// VF, marks it as used, then returns it's pci address including vf number
// TODO: this should be done in compatible way with other mechanisms
// (sr-iov cni?) which could also allocate devices by they own
// WARNING: this implementation is only for PoC purpose, it lacks proper
// transactional way of retrieving/storing info about already used devices
func AllocateDeviceOn(devNames []string) (string, error) {
	allVFs, err := getAllVFs(devNames)
	if err != nil {
		return "", err
	}

	fileAccessMutex.Lock()
	defer fileAccessMutex.Unlock()

	var allocatedDevicesBytes []byte
	var allocatedDevices map[string]bool
	allocatedDevices = make(map[string]bool)

	if _, err := os.Stat(AllocatedDevicesStorePath); os.IsExist(err) {
		allocatedDevicesBytes, err := ioutil.ReadFile(AllocatedDevicesStorePath)
		if err != nil {
			return "", err
		}
		for _, device := range strings.Split(string(allocatedDevicesBytes), ", ") {
			allocatedDevices[device] = true
		}
	}

	for _, device := range allVFs {
		if _, ok := allocatedDevices[device]; ok {
			continue
		}
		if err := ioutil.WriteFile(
			AllocatedDevicesStorePath,
			[]byte(string(allocatedDevicesBytes)+", "+device),
			0600,
		); err != nil {
			return "", err
		}
		return device, nil
	}

	return "", errors.New("All VFs already used")
}

// DeallocateDevice removes device pointed by parameter from list of devices
// already used
func DeallocateDevice(devName string) error {
	fileAccessMutex.Lock()
	defer fileAccessMutex.Unlock()

	devicesBytes, err := ioutil.ReadFile(AllocatedDevicesStorePath)
	if err != nil {
		return err
	}
	allocatedDevices := strings.Split(string(devicesBytes), ", ")

	// remove element from slice
	filteredDevices := allocatedDevices[:0]
	for _, device := range allocatedDevices {
		if device != devName {
			filteredDevices = append(filteredDevices, device)
		}
	}
	return ioutil.WriteFile(AllocatedDevicesStorePath, []byte(strings.Join(filteredDevices, ", ")), 0644)
}

// AddDeviceToDomainConf adds to domain definition hostdev type interface
// pointed by address passed as first argument
func AddDeviceToDomainConf(devAddress string, domainConf *libvirtxml.Domain) {
	address := newPCISourceAddress(devAddress)
	domainConf.Devices.Interfaces = append(
		domainConf.Devices.Interfaces,
		libvirtxml.DomainInterface{
			Type: "hostdev",
			Source: &libvirtxml.DomainInterfaceSource{
				Address: address.asDomainInterfaceSourceAddress(),
			},
			// try to use legacy driver as default vfio fails on test lab
			Driver: &libvirtxml.DomainInterfaceDriver{
				Name: "kvm",
			},
		},
	)
}

func getAllVFs(devNames []string) ([]string, error) {
	var devices []string

	for _, master := range devNames {
		devicePath := "/sys/class/net/" + master + "/device"
		paths, err := filepath.Glob(devicePath + "/virtfn*")
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			realPath, err := os.Readlink(path)
			if err != nil {
				return nil, err
			}
			devices = append(devices, filepath.Base(realPath))
		}
	}
	return devices, nil
}

type sourceAddress struct {
	type_    string
	domain   string
	bus      string
	slot     string
	function string
}

func newPCISourceAddress(devAddress string) *sourceAddress {
	return newSourceAddress("pci", devAddress)
}

func newSourceAddress(type_, devAddress string) *sourceAddress {
	dottedParts := strings.SplitN(devAddress, ".", 2)
	parts := strings.SplitN(dottedParts[0], ":", 3)
	return &sourceAddress{
		type_:    type_,
		domain:   "0x" + parts[0],
		bus:      "0x" + parts[1],
		slot:     "0x" + parts[2],
		function: "0x" + dottedParts[1],
	}
}

func (s *sourceAddress) asDomainInterfaceSourceAddress() *libvirtxml.DomainInterfaceSourceAddress {
	return &libvirtxml.DomainInterfaceSourceAddress{
		Type:     s.type_,
		Domain:   s.domain,
		Bus:      s.bus,
		Slot:     s.slot,
		Function: s.function,
	}
}
