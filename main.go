package main

import (
	"context"
	stdlog "log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"flag"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

func main() {
	stdlog.Println("Application starting up...")
	// Setup signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Check if we should run as worker or server
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "worker":
			workerCmd := flag.NewFlagSet("worker", flag.ExitOnError)
			checkConnection := workerCmd.Bool("check-connection", false, "check temporal connection and exit")
			workerCmd.Parse(os.Args[2:])

			if *checkConnection {
				checkTemporalConnection(ctx)
				return
			}
			runWorker(ctx, nil)
		case "server":
			runServer(ctx, stop, nil)
		case "terminate":
			terminateCmd := flag.NewFlagSet("terminate", flag.ExitOnError)
			workflowID := terminateCmd.String("id", "", "workflow ID to terminate (required)")
			reason := terminateCmd.String("reason", "Manual termination via CLI", "reason for termination")
			terminateCmd.Parse(os.Args[2:])

			if *workflowID == "" {
				stdlog.Fatalf("workflow ID is required. Usage: %s terminate -id <workflow-id> [-reason <reason>]", os.Args[0])
			}

			c := newTemporalClient()
			defer c.Close()

			err := TerminateWorkflow(c, *workflowID, *reason)
			if err != nil {
				stdlog.Fatalf("Failed to terminate workflow: %v", err)
			}
			stdlog.Printf("Successfully terminated workflow: %s", *workflowID)
		case "setup-bucket":
			setupBucketCmd := flag.NewFlagSet("setup-bucket", flag.ExitOnError)
			setupBucketCmd.Parse(os.Args[2:])

			provider := os.Getenv("STORAGE_PROVIDER")
			bucket := os.Getenv("STORAGE_BUCKET")

			storage := NewObjectStorage(provider)
			s3Storage, ok := storage.(*S3CompatibleStorage)
			if !ok {
				stdlog.Fatalf("setup-bucket only works with S3-compatible storage")
			}

			err := s3Storage.SetupBucketPublicRead(context.Background(), bucket)
			if err != nil {
				stdlog.Fatalf("Failed to setup bucket: %v", err)
			}
			stdlog.Printf("Successfully configured bucket '%s' for public read access", bucket)
		default:
			stdlog.Fatalf("Unknown command: %s", os.Args[1])
		}
	} else {
		// No arguments, run both for development
		var wg sync.WaitGroup
		wg.Add(2)

		stdlog.Println("🚀 Starting server and worker for development...")
		go runWorker(ctx, &wg)
		runServer(ctx, stop, &wg)

		wg.Wait()
		stdlog.Println("All services shut down.")
	}
}

func runWorker(ctx context.Context, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	// Create Temporal client
	c := newTemporalClient()
	defer c.Close()

	// Create worker
	w := worker.New(c, os.Getenv("TEMPORAL_TASK_QUEUE"), worker.Options{})

	// Register workflows and activities
	w.RegisterWorkflow(RunContentGenerationWorkflow)
	w.RegisterWorkflow(AgenticScrapeGitHubProfileWorkflow)
	w.RegisterWorkflow(PollWorkflow)
	w.RegisterWorkflow(GeneratePollImagesWorkflow)
	w.RegisterActivity(GenerateContentGenerationPrompt)
	w.RegisterActivity(GenerateContent)
	w.RegisterActivity(StoreContent)
	w.RegisterActivity(ExecuteGhCommandActivity)
	w.RegisterActivity(GenerateResponsesTurnActivity)
	w.RegisterActivity(CopyObject)

	// Start worker
	stdlog.Println("Starting worker...")
	err := w.Run(worker.InterruptCh())
	if err != nil {
		stdlog.Fatalln("Unable to start worker", err)
	}
	stdlog.Println("Worker shut down.")
}

func runServer(ctx context.Context, stop context.CancelFunc, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	// Create Temporal client
	c := newTemporalClient()
	defer c.Close()

	// Create API server
	provider := os.Getenv("STORAGE_PROVIDER")
	storage := NewObjectStorage(provider)
	apiServer := NewAPIServer(c, storage)

	// Setup routes
	router := apiServer.SetupRoutes()

	// Start server
	port := os.Getenv("PORT")

	// Start server in a goroutine
	go func() {
		stdlog.Printf("Starting server on port %s", port)
		if err := router.Start(":" + port); err != nil && err != http.ErrServerClosed {
			stdlog.Fatalf("listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()

	// Restore default behavior on the interrupt signal and notify user of shutdown.
	stop()
	stdlog.Println("Shutting down gracefully, press Ctrl+C again to force")

	// The context is used to inform the server it has 5 seconds to finish
	// the requests it is currently handling
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := router.Shutdown(shutdownCtx); err != nil {
		stdlog.Fatalf("Server forced to shutdown: %v", err)
	}

	stdlog.Println("Server exiting")
}

func checkTemporalConnection(ctx context.Context) {
	stdlog.Println("Checking Temporal connection...")
	c := newTemporalClient()
	defer c.Close()

	// Use CheckHealth for a more robust connection check
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second) // 5s timeout for the check
	defer cancel()
	_, err := c.CheckHealth(healthCtx, &client.CheckHealthRequest{})
	if err != nil {
		stdlog.Fatalf("Temporal health check failed: %v", err)
	}

	stdlog.Println("Temporal connection health check successful.")
}

func newTemporalClient() client.Client {
	var c client.Client
	var err error

	// Configure a logger for the Temporal client
	temporalLogger := log.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	hostPort := os.Getenv("TEMPORAL_HOST")
	if hostPort == "" {
		hostPort = "localhost:7233"
	}
	clientOptions := client.Options{
		HostPort:  hostPort,
		Namespace: "default",
		Logger:    temporalLogger,
	}

	// Retry connecting to Temporal with a backoff
	for i := 0; i < 10; i++ {
		c, err = client.Dial(clientOptions)
		if err == nil {
			stdlog.Println("Successfully connected to Temporal.")
			return c
		}
		stdlog.Printf("Unable to create client: %v. Retrying in 5 seconds...", err)
		time.Sleep(5 * time.Second)
	}

	stdlog.Fatalln("Unable to create Temporal client after multiple retries", err)
	return nil
}
