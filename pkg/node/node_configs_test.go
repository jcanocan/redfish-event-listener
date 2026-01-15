/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright The KubeVirt Authors.
 *
 */

package node_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/0xfelix/redfish-event-listener/pkg/node"
)

func newDynamicFakeForList(list *unstructured.UnstructuredList, gvr schema.GroupVersionResource) *dynamicfake.FakeDynamicClient {
	objs := make([]runtime.Object, 0, len(list.Items))
	for i := range list.Items {
		item := list.Items[i]
		objs = append(objs, &item)
	}
	listKinds := map[schema.GroupVersionResource]string{
		gvr: "FenceAgentsRemediationTemplateList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
}

func makeFAR(gv schema.GroupVersion, namespace, name, agent string, nodeparams map[string]interface{}) unstructured.Unstructured {
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": gv.String(),
			"kind":       "FenceAgentsRemediationTemplate",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"agent":          agent,
						"nodeparameters": nodeparams,
					},
				},
			},
		},
	}
}

func makeFARList(gv schema.GroupVersion, items []unstructured.Unstructured) *unstructured.UnstructuredList {
	return &unstructured.UnstructuredList{
		Object: map[string]interface{}{
			"apiVersion": gv.String(),
			"kind":       "FenceAgentsRemediationTemplateList",
		},
		Items: items,
	}
}

var _ = Describe("Node configs", func() {
	Context("from FenceAgentsRemediationTemplate", func() {
		const (
			namespace = "test-namespace"
			group     = "fence-agents-remediation.medik8s.io"
			version   = "v1alpha1"
		)
		var (
			gv  schema.GroupVersion
			gvr schema.GroupVersionResource
		)

		BeforeEach(func() {
			gv = schema.GroupVersion{Group: group, Version: version}
			gvr = schema.GroupVersionResource{Group: group, Version: version, Resource: "fenceagentsremediationtemplates"}
		})

		It("should return node configs for valid fence_ipmilan entries in the namespace", func() {
			list := makeFARList(gv, []unstructured.Unstructured{
				makeFAR(gv, namespace, "valid-far", "fence_ipmilan", map[string]interface{}{
					"--ip":       map[string]interface{}{"nodeA": "10.0.0.1", "nodeB": "10.0.0.2"},
					"--username": map[string]interface{}{"nodeA": "admin", "nodeB": "user"},
					"--password": map[string]interface{}{"nodeA": "pass", "nodeB": "pwd"},
				}),
				makeFAR(gv, "other", "out-of-ns", "fence_ipmilan", map[string]interface{}{
					"--ip":       map[string]interface{}{"nodeX": "192.0.2.1"},
					"--username": map[string]interface{}{"nodeX": "x"},
					"--password": map[string]interface{}{"nodeX": "y"},
				}),
				makeFAR(gv, namespace, "ignored-agent", "other_agent", map[string]interface{}{
					"--ip":       map[string]interface{}{"nodeZ": "192.0.2.2"},
					"--username": map[string]interface{}{"nodeZ": "z"},
					"--password": map[string]interface{}{"nodeZ": "w"},
				}),
			})

			dyn := newDynamicFakeForList(list, gvr)

			cfgs, err := node.GetNodesConfigFromFARConfig(dyn, namespace, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgs).To(ConsistOf(
				node.NodeConfig{
					NodeName: "nodeA",
					URL:      "https://10.0.0.1",
					Username: "admin",
					Password: "pass",
					Insecure: true,
				},
				node.NodeConfig{
					NodeName: "nodeB",
					URL:      "https://10.0.0.2",
					Username: "user",
					Password: "pwd",
					Insecure: true,
				},
			))
		})

		It("should return empty list when it is not in the expected namespace", func() {
			list := makeFARList(gv, []unstructured.Unstructured{
				makeFAR(gv, "other", "out-of-ns", "fence_ipmilan", map[string]interface{}{
					"--ip":       map[string]interface{}{"n1": "10.0.0.1"},
					"--username": map[string]interface{}{"n1": "u"},
					"--password": map[string]interface{}{"n1": "p"},
				}),
			})
			dyn := newDynamicFakeForList(list, gvr)
			cfgs, err := node.GetNodesConfigFromFARConfig(dyn, namespace, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgs).To(BeEmpty())
		})

		It("should return empty list when the agent is not fence_ipmilan", func() {
			list := makeFARList(gv, []unstructured.Unstructured{
				makeFAR(gv, namespace, "wrong-agent", "another-agent", map[string]interface{}{
					"--ip":       map[string]interface{}{"n1": "10.0.0.1"},
					"--username": map[string]interface{}{"n1": "u"},
					"--password": map[string]interface{}{"n1": "p"},
				}),
			})
			dyn := newDynamicFakeForList(list, gvr)
			cfgs, err := node.GetNodesConfigFromFARConfig(dyn, namespace, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgs).To(BeEmpty())
		})

		It("should skip nodes missing username/password entries and returns only complete ones", func() {
			list := makeFARList(gv, []unstructured.Unstructured{
				makeFAR(gv, namespace, "partial", "fence_ipmilan", map[string]interface{}{
					"--ip":       map[string]interface{}{"n1": "10.0.0.1", "n2": "10.0.0.2"},
					"--username": map[string]interface{}{"n1": "u1"},
					"--password": map[string]interface{}{"n1": "p1"},
				}),
			})
			dyn := newDynamicFakeForList(list, gvr)
			cfgs, err := node.GetNodesConfigFromFARConfig(dyn, namespace, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfgs).To(ConsistOf(node.NodeConfig{
				NodeName: "n1",
				URL:      "https://10.0.0.1",
				Username: "u1",
				Password: "p1",
				Insecure: true,
			},
			))
		})

		It("returns error when required maps are missing", func() {
			// Missing --username
			list := makeFARList(gv, []unstructured.Unstructured{
				makeFAR(gv, namespace, "missing-user", "fence_ipmilan", map[string]interface{}{
					"--ip":       map[string]interface{}{"n1": "10.0.0.1"},
					"--password": map[string]interface{}{"n1": "p1"},
				}),
			})
			dyn := newDynamicFakeForList(list, gvr)
			_, err := node.GetNodesConfigFromFARConfig(dyn, namespace, false)
			Expect(err).To(MatchError(ContainSubstring("failed to find '--username' parameter")))
		})
	})
})
