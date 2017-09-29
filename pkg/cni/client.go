/*
Copyright 2016 Mirantis

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

package cni

import (
	"fmt"

	"github.com/containernetworking/cni/libcni"
	cnicurrent "github.com/containernetworking/cni/pkg/types/current"
	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"

	"github.com/Mirantis/virtlet/pkg/utils"
)

type Client struct {
	pluginsInterface libcni.CNIConfig
	configuration    *libcni.NetworkConfig
}

func NewClient(pluginsDir, configsDir string) (*Client, error) {
	configuration, err := ReadConfiguration(configsDir)
	glog.V(3).Infof("CNI config: name: %q type: %q", configuration.Network.Name, configuration.Network.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to read CNI configuration: %v", err)
	}

	return &Client{
		pluginsInterface: libcni.CNIConfig{Path: []string{pluginsDir}},
		configuration:    configuration,
	}, nil
}

func (c *Client) Type() string { return c.configuration.Network.Type }

func (c *Client) cniRuntimeConf(podId, podName, podNs string) *libcni.RuntimeConf {
	r := &libcni.RuntimeConf{
		ContainerID: podId,
		NetNS:       PodNetNSPath(podId),
		IfName:      "virtlet-eth0",
	}
	if podName != "" && podNs != "" {
		r.Args = [][2]string{
			{"IgnoreUnknown", "1"},
			{"K8S_POD_NAMESPACE", podNs},
			{"K8S_POD_NAME", podName},
			{"K8S_POD_INFRA_CONTAINER_ID", podId},
		}
	}
	return r
}

func (c *Client) GetDummyNetwork() (*cnicurrent.Result, error) {
	// TODO: document the function
	// TODO: the fake pod id should be generated in a way
	// to make it unique for each node. Then a check should
	// be made to see whether the corresponding netns is
	// alive. If it is, it should be reused, otherwise
	// a new one with the same id should be created.
	podId := utils.NewUuid()
	if err := CreateNetNS(podId); err != nil {
		return nil, fmt.Errorf("couldn't create netns for fake pod %q: %v", podId, err)
	}
	return c.AddSandboxToNetwork(podId, "", "")
}

func (c *Client) AddSandboxToNetwork(podId, podName, podNs string) (*cnicurrent.Result, error) {
	cniConf := c.cniRuntimeConf(podId, podName, podNs)
	glog.V(3).Infof("AddSandboxToNetwork: podId %q, podName %q, podNs %q, config:\n%s",
		podId, podName, podNs, spew.Sdump(cniConf))
	result, err := c.pluginsInterface.AddNetwork(c.configuration, cniConf)
	if err == nil {
		glog.V(3).Infof("AddSandboxToNetwork: podId %q, podName %q, podNs %q: result:\n%s",
			podId, podName, podNs, spew.Sdump(result))
	} else {
		glog.V(3).Infof("AddSandboxToNetwork: podId %q, podName %q, podNs %q: error: %v",
			podId, podName, podNs, err)
		return nil, err
	}
	r, err := cnicurrent.NewResultFromResult(result)
	if err != nil {
		return nil, fmt.Errorf("error converting CNI result to the current version: %v", err)
	}
	return r, err
}

func (c *Client) RemoveSandboxFromNetwork(podId, podName, podNs string) error {
	glog.V(3).Infof("RemoveSandboxFromNetwork: podId %q, podName %q, podNs %q", podId, podName, podNs)
	err := c.pluginsInterface.DelNetwork(c.configuration, c.cniRuntimeConf(podId, podName, podNs))
	if err == nil {
		glog.V(3).Infof("RemoveSandboxFromNetwork: podId %q, podName %q, podNs %q: success",
			podId, podName, podNs)
	} else {
		glog.V(3).Infof("RemoveSandboxFromNetwork: podId %q, podName %q, podNs %q: error: %v",
			podId, podName, podNs, err)
	}
	return err
}
