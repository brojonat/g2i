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
			runWorker(ctx, nil)
		case "server":
			runServer(ctx, stop, nil)
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
	w := worker.New(c, "content-generation-queue", worker.Options{})

	// Register workflows and activities
	w.RegisterWorkflow(RunContentGenerationWorkflow)
	w.RegisterActivity(ScrapeGitHubProfile)
	w.RegisterActivity(GeneratePrompt)
	w.RegisterActivity(GenerateContent)
	w.RegisterActivity(StoreContent)

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
	apiServer := NewAPIServer(c)

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
		stdlog.Fatalf("Server forced to shutdown: ", err)
	}

	stdlog.Println("Server exiting")
}

func newTemporalClient() client.Client {
	var c client.Client
	var err error

	// Configure a logger for the Temporal client
	temporalLogger := log.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	clientOptions := client.Options{
		HostPort:  "localhost:7233",
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
