package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/redfish"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	envListenerAddr   = "LISTENER_ADDR"
	envListenerPort   = "LISTENER_PORT"
	envDestinationURL = "DESTINATION_URL"
	envNodeName       = "NODE_NAME"

	envRedfishURL      = "REDFISH_URL"
	envRedfishUser     = "REDFISH_USER"
	envRedfishPass     = "REDFISH_PASS"
	envRedfishInsecure = "REDFISH_INSECURE"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	k8sClient, err := createK8sClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	nodeName := lookupEnv(envNodeName)
	log.Printf("Monitoring node: %s", nodeName)

	redfishClient, err := createRedfishClient()
	if err != nil {
		return fmt.Errorf("failed to get Redfish client: %w", err)
	}
	defer redfishClient.Logout()

	subscription, err := createSubscription(redfishClient, lookupEnv(envDestinationURL))
	if err != nil {
		return fmt.Errorf("failed to create event subscription: %w", err)
	}
	log.Printf("Created Redfish event subscription: %s", subscription.ID)

	s := startServer(func(w http.ResponseWriter, r *http.Request) {
		handleRedfishEvent(w, r, k8sClient, nodeName)
	})

	quit, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	<-quit.Done()

	log.Printf("Deleting Redfish event subscription: %s", subscription.ID)
	if err := redfish.DeleteEventDestination(redfishClient, subscription.ODataID); err != nil {
		log.Printf("Failed to delete Redfish event subscription: %v", err)
	}

	log.Println("Shutting down Redfish event listener")
	const shutdownTimeout = 5
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown error: %w", err)
	}

	return nil
}

func lookupEnv(key string) string {
	val, ok := os.LookupEnv(key)
	if !ok {
		log.Fatalf("Environment variable %s not set", key)
	}
	return val
}

func lookupInsecure() bool {
	val, ok := os.LookupEnv(envRedfishInsecure)
	if !ok {
		return false
	}
	insecure, err := strconv.ParseBool(val)
	if err != nil {
		log.Fatalf("Invalid value %s for environment variable REDFISH_INSECURE", val)
	}
	return insecure
}

func createK8sClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	return kubernetes.NewForConfig(config)
}

func createRedfishClient() (*gofish.APIClient, error) {
	config := gofish.ClientConfig{
		Endpoint:  lookupEnv(envRedfishURL),
		Username:  lookupEnv(envRedfishUser),
		Password:  lookupEnv(envRedfishPass),
		Insecure:  lookupInsecure(),
		BasicAuth: true,
	}

	return gofish.Connect(config)
}

func createSubscription(client *gofish.APIClient, destinationURL string) (*redfish.EventDestination, error) {
	service, err := client.GetService().EventService()
	if err != nil {
		return nil, fmt.Errorf("failed to get EventService: %w", err)
	}

	uri, err := redfish.CreateEventDestinationInstance(
		client, service.Subscriptions, destinationURL,
		nil, nil, nil,
		redfish.RedfishEventDestinationProtocol, "RedfishEventListener",
		redfish.RetryForeverDeliveryRetryPolicy, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create event subscription: %w", err)
	}

	dest, err := redfish.GetEventDestination(client, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to get created event subscription: %w", err)
	}

	return dest, nil
}

func startServer(handler http.HandlerFunc) *http.Server {
	const (
		addr              = "0.0.0.0:8080"
		readHeaderTimeout = 10
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	s := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout * time.Second,
	}

	go func() {
		log.Printf("Starting Redfish event listener on %s", addr)
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	return s
}

func handleRedfishEvent(w http.ResponseWriter, r *http.Request, k8sClient *kubernetes.Clientset, nodeName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event redfish.Event
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&event); err != nil {
		log.Printf("Error decoding event: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("Error closing request body: %v", err)
		}
	}()

	log.Printf("Received Redfish event:")
	log.Printf("  ID: %s", event.ID)
	log.Printf("  Name: %s", event.Name)
	log.Printf("  Context: %s", event.Context)
	log.Printf("  Number of events: %d", len(event.Events))

	for i, ev := range event.Events {
		log.Printf("  Event %d:", i+1)
		log.Printf("    EventType: %s", ev.EventType)
		log.Printf("    EventID: %s", ev.EventID)
		log.Printf("    Severity: %s", ev.Severity)
		log.Printf("    Message: %s", ev.Message)
		log.Printf("    MessageID: %s", ev.MessageID)
		log.Printf("    Timestamp: %s", ev.EventTimestamp)

		if strings.Contains(ev.MessageID, "ASR0001") {
			log.Printf("Detected ASR0001 event, updating node condition for %s", nodeName)
			if err := updateNodeCondition(k8sClient, nodeName); err != nil {
				log.Printf("Failed to update node condition: %v", err)
			} else {
				log.Printf("Successfully updated node condition for %s", nodeName)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("Event received")); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

func updateNodeCondition(k8sClient *kubernetes.Clientset, nodeName string) error {
	const conditionType = "TestCondition"

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
