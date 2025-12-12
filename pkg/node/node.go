package node

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	conditionType = "TestCondition"
)

func UpdateNodeCondition(k8sClient *kubernetes.Clientset, nodeName string) error {
	node, err := k8sClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	now := metav1.Now()
	newCondition := corev1.NodeCondition{
		Type:               conditionType,
		Status:             corev1.ConditionFalse,
		LastHeartbeatTime:  now,
		LastTransitionTime: now,
		Reason:             "EventReceived",
		Message:            "Redfish event ASR0001 received",
	}

	conditionExists := false
	for i, condition := range node.Status.Conditions {
		if condition.Type == conditionType {
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
