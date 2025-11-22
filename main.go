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

// appConfig is the global application configuration, loaded once at startup
var appConfig *Config

func main() {
	stdlog.Println("Application starting up...")

	// Load and validate configuration
	cfg, err := LoadConfig()
	if err != nil {
		stdlog.Fatalf("Failed to load configuration: %v", err)
	}
	appConfig = cfg // Set global config
	stdlog.Println("Configuration loaded and validated successfully")

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
				checkTemporalConnection(ctx, cfg)
				return
			}
			runWorker(ctx, cfg, nil)
		case "server":
			runServer(ctx, stop, cfg, nil)
		case "terminate":
			terminateCmd := flag.NewFlagSet("terminate", flag.ExitOnError)
			workflowID := terminateCmd.String("id", "", "workflow ID to terminate (required)")
			reason := terminateCmd.String("reason", "Manual termination via CLI", "reason for termination")
			terminateCmd.Parse(os.Args[2:])

			if *workflowID == "" {
				stdlog.Fatalf("workflow ID is required. Usage: %s terminate -id <workflow-id> [-reason <reason>]", os.Args[0])
			}

			c := newTemporalClient(cfg)
			defer c.Close()

			err := TerminateWorkflow(c, *workflowID, *reason)
			if err != nil {
				stdlog.Fatalf("Failed to terminate workflow: %v", err)
			}
			stdlog.Printf("Successfully terminated workflow: %s", *workflowID)
		case "setup-bucket":
			setupBucketCmd := flag.NewFlagSet("setup-bucket", flag.ExitOnError)
			setupBucketCmd.Parse(os.Args[2:])

			bucket := cfg.StorageBucket

			storage := NewObjectStorage(cfg)
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
		go runWorker(ctx, cfg, &wg)
		runServer(ctx, stop, cfg, &wg)

		wg.Wait()
		stdlog.Println("All services shut down.")
	}
}

func runWorker(ctx context.Context, cfg *Config, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	// Create Temporal client
	c := newTemporalClient(cfg)
	defer c.Close()

	// Create worker
	w := worker.New(c, cfg.TemporalTaskQueue, worker.Options{})

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
	w.RegisterActivity(WaitForPayment)

	// Start worker
	stdlog.Println("Starting worker...")
	err := w.Run(worker.InterruptCh())
	if err != nil {
		stdlog.Fatalln("Unable to start worker", err)
	}
	stdlog.Println("Worker shut down.")
}

func runServer(ctx context.Context, stop context.CancelFunc, cfg *Config, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	// Create Temporal client
	c := newTemporalClient(cfg)
	defer c.Close()

	// Create API server
	storage := NewObjectStorage(cfg)
	apiServer := NewAPIServer(c, storage, cfg)

	// Setup routes
	apiServer = apiServer.SetupRoutes()

	// Start server
	port := cfg.Port

	// Start server in a goroutine
	go func() {
		stdlog.Printf("Starting server on port %s", port)
		if err := apiServer.Start(":" + port); err != nil && err != http.ErrServerClosed {
			stdlog.Fatalf("listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()

	// Restore default behavior on the interrupt signal and notify user of shutdown.
	stop()
	stdlog.Println("Shutting down gracefully, press Ctrl+C again to force")

	// The context is used to inform the server it has 30 seconds to finish
	// the requests it is currently handling
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		stdlog.Fatalf("Server forced to shutdown: %v", err)
	}

	stdlog.Println("Server exiting")
}

func checkTemporalConnection(ctx context.Context, cfg *Config) {
	stdlog.Println("Checking Temporal connection...")
	c := newTemporalClient(cfg)
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

func newTemporalClient(cfg *Config) client.Client {
	var c client.Client
	var err error

	// Configure a logger for the Temporal client
	temporalLogger := log.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	clientOptions := client.Options{
		HostPort:  cfg.TemporalHost,
		Namespace: cfg.TemporalNamespace,
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
