package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"go.temporal.io/sdk/workflow"
)

// PollConfig is the configuration for a poll workflow.
type PollConfig struct {
	Question        string   // the question being asked
	AllowedVoters   []string // if empty, anyone can vote
	AllowedOptions  []string // if empty, any option can be voted for
	DurationSeconds int      // if 0, the poll will run indefinitely
	StartBlocked    bool     // if true, the poll will not start until a start_poll signal is received
	SingleVote      bool     // if true, a user can only vote once
	// Payment-related fields
	PaymentRequired bool    // if true, poll requires payment before accepting votes
	PaymentWallet   string  // Solana wallet address to receive payment
	PaymentAmount   float64 // Amount in SOL required for payment
}

// PollState is the dynamic state of a poll.
type PollState struct {
	Options      map[string]int
	Voters       map[string]struct{}
	PaymentPaid  bool   // true if payment has been received
	PaymentTxnID string // Solana transaction ID of the payment
}

// PollSummary is now defined in types.go

// --- Signal Structs ---

type AddVoterSignal struct{ UserID string }
type RemoveVoterSignal struct{ UserID string }
type AddOptionSignal struct{ Option string }
type RemoveOptionSignal struct{ Option string }

// PollWorkflow is the main workflow function for our configurable poll.
func PollWorkflow(ctx workflow.Context, config PollConfig) (PollSummary, error) {
	logger := workflow.GetLogger(ctx)

	state := PollState{
		Options: make(map[string]int),
		Voters:  make(map[string]struct{}),
	}
	var allowedVoters map[string]struct{}
	if config.AllowedVoters != nil {
		allowedVoters = make(map[string]struct{})
		for _, v := range config.AllowedVoters {
			allowedVoters[v] = struct{}{}
		}
	}
	var allowedOptions map[string]struct{}
	if config.AllowedOptions != nil {
		allowedOptions = make(map[string]struct{})
		for _, o := range config.AllowedOptions {
			allowedOptions[o] = struct{}{}
		}
	}

	// Set up query handlers...
	// (Query handler setup remains the same)
	err := workflow.SetQueryHandler(ctx, "get_state", func() (PollState, error) {
		return state, nil
	})
	if err != nil {
		return PollSummary{}, fmt.Errorf("failed to set get_state query handler: %w", err)
	}
	err = workflow.SetQueryHandler(ctx, "get_config", func() (PollConfig, error) {
		return config, nil
	})
	if err != nil {
		return PollSummary{}, fmt.Errorf("failed to set get_config query handler: %w", err)
	}
	err = workflow.SetQueryHandler(ctx, "get_voters", func() ([]string, error) {
		var voters []string
		if allowedVoters != nil {
			for v := range allowedVoters {
				voters = append(voters, v)
			}
		} else {
			for v := range state.Voters {
				voters = append(voters, v)
			}
		}
		sort.Strings(voters)
		return voters, nil
	})
	if err != nil {
		return PollSummary{}, fmt.Errorf("failed to set get_voters query handler: %w", err)
	}
	err = workflow.SetQueryHandler(ctx, "get_options", func() ([]string, error) {
		var options []string
		if allowedOptions != nil {
			for o := range allowedOptions {
				options = append(options, o)
			}
		} else {
			for o := range state.Options {
				options = append(options, o)
			}
		}
		sort.Strings(options)
		return options, nil
	})
	if err != nil {
		return PollSummary{}, fmt.Errorf("failed to set get_options query handler: %w", err)
	}

	err = workflow.SetUpdateHandler(ctx, "vote", func(ctx workflow.Context, update VoteUpdate) (VoteUpdateResult, error) {
		// Check if payment is required but not yet received
		if config.PaymentRequired && !state.PaymentPaid {
			return VoteUpdateResult{}, fmt.Errorf("poll requires payment before voting - please complete payment first")
		}
		if allowedVoters != nil {
			if _, ok := allowedVoters[update.UserID]; !ok {
				return VoteUpdateResult{}, fmt.Errorf("vote rejected for non-allowed voter: %s", update.UserID)
			}
		}
		if allowedOptions != nil {
			if _, ok := allowedOptions[update.Option]; !ok {
				return VoteUpdateResult{}, fmt.Errorf("vote rejected for non-allowed option: %s", update.Option)
			}
		}
		if _, ok := state.Voters[update.UserID]; ok && config.SingleVote {
			return VoteUpdateResult{}, fmt.Errorf("vote rejected for duplicate voter: %s", update.UserID)
		}
		state.Options[update.Option] += update.Amount
		state.Voters[update.UserID] = struct{}{}
		return VoteUpdateResult{TotalVotes: state.Options[update.Option]}, nil
	})
	if err != nil {
		return PollSummary{}, fmt.Errorf("failed to set vote update handler: %w", err)
	}

	// --- Main Workflow Logic ---
	if config.StartBlocked {
		logger.Info("Poll is blocked, waiting for start signal.")
		startChan := workflow.GetSignalChannel(ctx, "start_poll")
		startChan.Receive(ctx, nil) // Block until signal is received
		logger.Info("Poll started.")
	}

	// Wait for payment if required
	if config.PaymentRequired {
		logger.Info("Poll requires payment. Waiting for payment to be received...",
			"wallet", config.PaymentWallet,
			"amount", config.PaymentAmount)

		// Execute the WaitForPayment activity
		activityOptions := workflow.ActivityOptions{
			StartToCloseTimeout: 7 * 24 * time.Hour, // Max 7 days to receive payment
		}
		activityCtx := workflow.WithActivityOptions(ctx, activityOptions)

		var paymentOutput WaitForPaymentOutput
		workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

		paymentInput := WaitForPaymentInput{
			ForohtooServerURL: os.Getenv("FOROHTOO_SERVER_URL"),
			PaymentWallet:     config.PaymentWallet,
			Network:           getEnvOrDefault("SOLANA_NETWORK", "mainnet"),
			WorkflowID:        workflowID,
			ExpectedAmount:    config.PaymentAmount,
		}

		err = workflow.ExecuteActivity(activityCtx, WaitForPayment, paymentInput).Get(activityCtx, &paymentOutput)
		if err != nil {
			logger.Error("Payment wait failed", "error", err)
			return PollSummary{}, fmt.Errorf("failed to receive payment: %w", err)
		}

		// Update state to mark payment as received
		state.PaymentPaid = true
		state.PaymentTxnID = paymentOutput.TransactionID
		logger.Info("Payment received! Poll is now accepting votes.",
			"transactionID", paymentOutput.TransactionID,
			"amount", paymentOutput.Amount)
	}

	var timerFuture workflow.Future
	if config.DurationSeconds > 0 {
		timerFuture = workflow.NewTimer(ctx, time.Second*time.Duration(config.DurationSeconds))
	}

	exit := false
	for !exit {
		selector := workflow.NewSelector(ctx)

		if timerFuture != nil {
			selector.AddFuture(timerFuture, func(f workflow.Future) {
				logger.Info("Poll timed out.")
				exit = true
			})
		}

		selector.AddReceive(workflow.GetSignalChannel(ctx, "end_poll"), func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, nil)
			logger.Info("end_poll signal received. Exiting.")
			exit = true
		})

		// (rest of the signal handlers)
		selector.AddReceive(workflow.GetSignalChannel(ctx, "add_voter"), func(c workflow.ReceiveChannel, more bool) {
			var signal AddVoterSignal
			c.Receive(ctx, &signal)
			if allowedVoters != nil {
				allowedVoters[signal.UserID] = struct{}{}
			} else {
				logger.Warn("Signal 'add_voter' ignored on non-restricted poll.")
			}
		})

		selector.AddReceive(workflow.GetSignalChannel(ctx, "remove_voter"), func(c workflow.ReceiveChannel, more bool) {
			var signal RemoveVoterSignal
			c.Receive(ctx, &signal)
			if allowedVoters != nil {
				delete(allowedVoters, signal.UserID)
			} else {
				logger.Warn("Signal 'remove_voter' ignored on non-restricted poll.")
			}
		})

		selector.AddReceive(workflow.GetSignalChannel(ctx, "add_option"), func(c workflow.ReceiveChannel, more bool) {
			var signal AddOptionSignal
			c.Receive(ctx, &signal)
			if allowedOptions != nil {
				allowedOptions[signal.Option] = struct{}{}
			} else {
				logger.Warn("Signal 'add_option' ignored on non-restricted poll.")
			}
		})

		selector.AddReceive(workflow.GetSignalChannel(ctx, "remove_option"), func(c workflow.ReceiveChannel, more bool) {
			var signal RemoveOptionSignal
			c.Receive(ctx, &signal)
			if allowedOptions != nil {
				delete(allowedOptions, signal.Option)
			} else {
				logger.Warn("Signal 'remove_option' ignored on non-restricted poll.")
			}
		})

		selector.Select(ctx)

		if ctx.Err() != nil {
			logger.Info("Workflow context cancelled. Exiting loop.", "Error", ctx.Err())
			return PollSummary{}, ctx.Err()
		}
	}
	// the poll should return summary information to the client
	summary := PollSummary{
		Question: config.Question,
		Options:  state.Options,
		Voters:   state.Voters,
	}
	return summary, nil
}

// getEnvOrDefault returns the environment variable value or a default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
