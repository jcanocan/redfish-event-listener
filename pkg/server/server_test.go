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

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stmcginnis/gofish/redfish"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/0xfelix/redfish-event-listener/pkg/node"
)

const eventContextPrefix = "RedfishEventListener-"

var _ = Describe("Redish event server", func() {
	Context("handling functionality", func() {
		DescribeTable("should rejects invalid",
			func(makeReq func() *http.Request, expectedStatus int) {
				eventCh := make(chan redfish.Event, 1)
				rr := httptest.NewRecorder()
				req := makeReq()

				HandleRedfishEvent(rr, req, eventCh, eventContextPrefix)

				Expect(rr.Code).To(Equal(expectedStatus))
				Consistently(eventCh).ShouldNot(Receive())
			},
			Entry("POST requests",
				func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
				http.StatusMethodNotAllowed,
			),
			Entry("JSON requests",
				func() *http.Request { return httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not-json")) },
				http.StatusBadRequest,
			),
			Entry("event context requests",
				func() *http.Request {
					ev := redfish.Event{Context: "wrong-ctx"}
					body, _ := json.Marshal(ev)
					return httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
				},
				http.StatusBadRequest,
			),
		)

		It("should accept valid event and forwards it on channel", func() {
			ev := redfish.Event{
				Context: eventContextPrefix + "node01",
				Events: []redfish.EventRecord{
					{
						EventID:        "1",
						Message:        "test",
						MessageID:      "OTHER",
						EventType:      redfish.AlertEventType,
						EventTimestamp: time.Now().Format(time.RFC3339),
					},
				},
			}
			body, err := json.Marshal(ev)
			Expect(err).NotTo(HaveOccurred())

			eventCh := make(chan redfish.Event, 1)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			rr := httptest.NewRecorder()

			HandleRedfishEvent(rr, req, eventCh, eventContextPrefix)
			Expect(rr.Code).To(Equal(http.StatusOK))

			b, err := io.ReadAll(rr.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(ContainSubstring("Event received"))

			var received redfish.Event
			Eventually(eventCh).Should(Receive(&received))
			Expect(received.Context).To(Equal(ev.Context))
			Expect(received.Events).To(HaveLen(1))
		})
	})

	Context("watchdog event handling", func() {
		It("should not set a node condition for non-watchdog events", func() {
			nodeName := "node-1"
			cs := fake.NewSimpleClientset(
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName,
					},
				},
			)
			ev := &redfish.Event{
				Context: eventContextPrefix + nodeName,
				Events: []redfish.EventRecord{
					{
						EventID:   "sub-1",
						Message:   "something",
						MessageID: "NOT_WATCHDOG",
						Severity:  "OK",
					},
				},
			}

			HandleEvent(ev, cs, nodeName)

			n, err := cs.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			found := false
			for _, c := range n.Status.Conditions {
				if string(c.Type) == node.ConditionType {
					found = true
					break
				}
			}
			Expect(found).To(BeFalse(), "expected TestCondition to not be set for non-watchdog events")
		})

		It("should set a node condition for watchdog events", func() {
			nodeName := "node-1"
			cs := fake.NewSimpleClientset(
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName,
					},
				},
			)
			ev := &redfish.Event{
				Context: eventContextPrefix + nodeName,
				Events: []redfish.EventRecord{
					{
						EventID:   "sub-1",
						Message:   "something",
						MessageID: "ASR0001",
						Severity:  "OK",
					},
				},
			}
			HandleEvent(ev, cs, nodeName)

			n, err := cs.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			found := false
			for _, c := range n.Status.Conditions {
				if string(c.Type) == node.ConditionType {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected TestCondition to be set for watchdog events")
		})
	})
})
