package node

import (
	"context"
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	watchpkg "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	ConditionType = "TestCondition"
)

func UpdateNodeCondition(k8sClient kubernetes.Interface, nodeName string) error {
	node, err := k8sClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	now := metav1.Now()
	newCondition := corev1.NodeCondition{
		Type:               ConditionType,
		Status:             corev1.ConditionFalse,
		LastHeartbeatTime:  now,
		LastTransitionTime: now,
		Reason:             "EventReceived",
		Message:            "Redfish event ASR0001 received",
	}

	conditionExists := false
	for i, condition := range node.Status.Conditions {
		if condition.Type == ConditionType {
			node.Status.Conditions[i] = newCondition
			conditionExists = true
			break
		}
	}
	if !conditionExists {
		node.Status.Conditions = append(node.Status.Conditions, newCondition)
	}

	_, err = k8sClient.CoreV1().Nodes().UpdateStatus(context.Background(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node status: %w", err)
	}

	return nil
}

// CreateNodeConditionRemovalWatcher creates a watcher for the node condition removal.
// It creates a background watcher for the affected node which expects the following events:
//  1. Node has to reach the non-ready state as part of the remediation/reboot process
//  2. Node has to reach the ready state when it is remediated/rebooted
//
// After the node is ready, the node condition is removed and the watcher is stopped.
func CreateNodeConditionRemovalWatcher(k8sClient kubernetes.Interface, nodeName string) error {
	node, err := k8sClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	opts := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", nodeName).String(),
		ResourceVersion: node.GetResourceVersion(),
	}

	watch, err := k8sClient.CoreV1().Nodes().Watch(context.Background(), opts)
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	go func() {
		reachedNonReadyState := false
		for event := range watch.ResultChan() {
			if event.Type == watchpkg.Modified {
				node := event.Object.(*corev1.Node)
				for _, condition := range node.Status.Conditions {
					// 1. Node has to reach the non-ready state as part of the remediation process
					if !reachedNonReadyState && condition.Type == corev1.NodeReady &&
						(condition.Status == corev1.ConditionFalse || condition.Status == corev1.ConditionUnknown) {
						log.Printf("Node %s is not ready", nodeName)
						reachedNonReadyState = true
						break
					}
					// 2. Node has to reach the ready state as part of the remediation process
					if reachedNonReadyState && condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
						log.Printf("Node %s is ready", nodeName)
						// 3. Remove the node condition once the node is ready

						if err := RemoveNodeCondition(k8sClient, nodeName); err != nil {
							log.Printf("Failed to remove node condition: %v", err)
							// Let's allow the goroutine to try later again if something went wrong
							break
						}
						// 4. Stop the watch
						watch.Stop()
						return
					}
				}
			}
		}
	}()

	return nil
}

func RemoveNodeCondition(k8sClient kubernetes.Interface, nodeName string) error {
	node, err := k8sClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	for i, condition := range node.Status.Conditions {
		if condition.Type == ConditionType {
			node.Status.Conditions = append(node.Status.Conditions[:i], node.Status.Conditions[i+1:]...)
			break
		}
	}

	_, err = k8sClient.CoreV1().Nodes().UpdateStatus(context.Background(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node status: %w", err)
	}
	return nil
}
