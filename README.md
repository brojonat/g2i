# GitHub-to-Image-to-Invitational-Poll

TODO: add teardown function to workflows that will delete the poll folder and all its contents.
TODO: on submission of a poll, we should immediately move to the next page, and if payment is required, we should only show the solana qr code, we don't need to show the image statuses or the votes.
TODO: only return active polls to the list page.
TODO: only return polls that are in that environment to the list page (we probably need to namespace or task-queue specific to the environment).

A Temporal-based service that aggregates GitHub profiles and generates representational content anchored in modern cultural context. Perfect for candidate screening, developer showcases, or cultural representation projects.

## Overview

This service takes a github username, scrapes their profile data (this should be agentic in the sense that we have an agent in a loop that can use the github cli until it's satisfied that has collected sufficient data), generates a visual representation using AI, and creates a voting poll for community interaction. The entire process is orchestrated through Temporal workflows for reliability and scalability.

## Architecture

The service consists of several key components:

- **Temporal Workflows**: Orchestrate the entire process
- **GitHub Scraping**: Extracts profile data, repositories, and code snippets. This should be agentic in the sense that we have an agent in a loop that can use the github cli until it's satisfied that has collected sufficient data.
- **AI Content Generation**: Creates visual representations using frontier models
- **Object Storage**: Storage-agnostic interface with S3-compatible storage as default (supports Minio, AWS S3, DigitalOcean Spaces, etc.)
- **Polling System**: Enables community voting and interaction
- **HTMX Web Interface**: Hypermedia-driven web interface for triggering workflows and voting

## Quick Start

### Prerequisites

- Go 1.21+
- Temporal server running
- OpenAI API key
- Object storage (S3-compatible by default, supports Minio, AWS S3, DigitalOcean Spaces, etc.)

### Installation

1. Clone the repository:

```bash
git clone <repository-url>
cd github-to-img-to-invitational-poll
```

2. Install dependencies:

```bash
make deps
```

3. Set up environment variables:

```bash
cp env.example .env
# Edit .env with your credentials
```

4. Start Temporal server:

```bash
make start-temporal
```

5. Start S3-compatible storage server (in another terminal):

```bash
make start-minio
```

6. Run the worker (in another terminal):

```bash
make run-worker
```

7. Run the API server (in another terminal):

```bash
make run-server
```

**Note**: The application now uses a unified binary. You can also run:

- `go run main.go worker` - Start the Temporal worker
- `go run main.go server` - Start the API server

### Usage

#### Web Interface

The service provides a modern web interface powered by HTMX:

1. **Home Page**: Visit `http://localhost:8080` to start a new visualization
2. **Workflow Tracking**: Real-time updates on workflow progress
3. **Voting Interface**: Interactive polls for community voting
4. **Results Display**: Visual representation of generated content and vote results

#### API Endpoints (HTMX)

- `GET /` - Home page with visualization form
- `POST /generate` - Start content generation workflow (HTMX form submission)
- `GET /workflow/:id/status` - Get workflow status (HTMX partial)
- `GET /workflow/:id` - Full workflow details page
- `GET /poll/:id` - Poll page with voting interface
- `POST /poll/:id/vote` - Submit a vote (HTMX form submission)

## Workflow Details

### Content Generation Workflow

The main workflow (`RunContentGenerationWorkflow`) performs these steps:

1. **GitHub Profile Scraping**: Extracts comprehensive profile data including:

   - Basic profile information (bio, location, website)
   - Repository statistics (original vs forked repos)
   - Language usage and top repositories
   - Code snippets and contribution patterns
   - Professional score and safety flags

2. **Prompt Generation**: Creates detailed prompts for AI content generation based on:

- Profile summary and technical interests
- Repository descriptions and code style
- Professional assessment and contribution patterns

Basically, this creates a "report card" for the developer.

3. **Content Generation**: Uses frontier models (DALL-E, etc.) to create visual representations that grounds the profile in modern cultural context. For instance, draw this developer as one of the three dragons meme, or put this developer on the bell curve meme. Or just generate good vibes images or bad vibes images accordingly. In other words, put it in cultural context.

4. **Content Storage**: Stores generated content using a storage-agnostic interface. Defaults to S3-compatible storage for local development, but supports AWS S3, GCS, and other object storage backends. Images are stored for posterity and better performance.

5. **Poll Creation**: Sets up a voting poll for community interaction

### Poll Workflow

The poll workflow (`RunPollWorkflow`) manages:

- Vote collection and validation
- Poll expiration handling
- Result aggregation
- User authentication (optional)

## Web Interface Features

- **HTMX-Powered**: Modern, responsive web interface without JavaScript frameworks
- **Real-time Updates**: Live workflow progress tracking
- **Interactive Voting**: Seamless voting experience with instant feedback
- **Mobile-Friendly**: Responsive design that works on all devices
- **Hypermedia Navigation**: Traditional web navigation with enhanced interactivity

## Configuration

### Environment Variables

- `TEMPORAL_HOST`: Temporal server address
- `OPENAI_API_KEY`: OpenAI API key for content generation
- `S3_ENDPOINT`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_USE_SSL`: S3-compatible storage credentials (default storage)
- `STORAGE_PROVIDER`, `STORAGE_BUCKET`: Default storage settings
- `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`: AWS credentials for S3
- `GOOGLE_APPLICATION_CREDENTIALS`: GCS service account file
- `PORT`: HTTP server port (default: 8080)

### Input Parameters

The `AppInput` struct supports:

- `ResearchAgentSystemPrompt`: Prompt for the agentic github scraping process. Specifies goals, objectives, and constraints...
- `ContentGenerationSystemPrompt`: Prompt for the content generation process. Specifies goals, objectives, and constraints...`
- `ModelName`: AI model to use (e.g., "dall-e-3")
- `StorageProvider`: Storage backend ("s3" default for S3-compatible, "aws-s3", "gcs")
- `StorageBucket`: Storage bucket name
- `PollSettings`: Poll configuration

## Development

### Building

```bash
make build
```

### Running Tests

```bash
make test
```

### Cleanup

```bash
make clean
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

[Add your license here]
