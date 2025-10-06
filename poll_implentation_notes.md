# Poll Implementation Notes (HTMX / Hypermedia API)

This document outlines the implementation of a poll feature using a hypermedia-driven API with HTMX. The API will accept standard HTML form submissions and respond with HTML fragments to dynamically update the user interface.

## 1. Poll Creation

### `GET /poll/new`

- **Description**: Renders a page with a form for creating a new poll.
- **Template**: `poll-form.html`
- **Response**: A full HTML document displaying the poll creation form.

### `POST /poll`

- **Description**: Handles the submission of the new poll form. It creates the `PollWorkflow` and redirects the user to the newly created poll's page.
- **Request Body**: `application/x-www-form-urlencoded` from the HTML form.
  - `question`: The poll question.
  - `allowed_options`: Comma-separated list of options.
  - `duration_seconds`: Duration of the poll in seconds.
  - ... (other config fields)
- **Response**: An HTTP 302 redirect to `GET /poll/{workflow_id}`.

## 2. Viewing and Voting on a Poll

### `GET /poll/{id}`

- **Description**: Displays the poll's details, including the question, options for voting, and the current results.
- **URL Parameters**:
  - `id`: The workflow ID of the poll.
- **Template**: `poll-details.html`
- **Response**: A full HTML document. The page will include:
  - The poll question.
  - A form to submit a vote (`POST /poll/{id}/vote`).
  - A section to display the current results. This section can be configured to auto-refresh using HTMX polling attributes (`hx-trigger="every 5s"`).

### `POST /poll/{id}/vote`

- **Description**: Submits a vote for a specific poll option. This endpoint is designed to be called via HTMX.
- **URL Parameters**:
  - `id`: The workflow ID of the poll.
- **Request Body**: `application/x-www-form-urlencoded` from the voting form.
  - `option`: The option being voted for.
  - `user_id`: The ID of the user voting (for now, this could be a simple text input).
- **Template**: `poll-results.html` (or a block within `poll-details.html`)
- **Response**: An HTML fragment containing the updated poll results. HTMX will swap this fragment into the appropriate place on the page (e.g., inside a `<div id="poll-results">`).

## 3. Poll Management (Admin)

### `GET /poll/{id}/manage`

- **Description**: Displays a management page for the poll, allowing an admin to perform actions like ending the poll or managing voters/options.
- **Template**: `poll-manage.html`

### Management Actions (called via HTMX from the management page)

The following endpoints will be triggered by buttons on the management page and will return HTML fragments to update the UI, providing feedback to the admin.

- **`POST /poll/{id}/start`**: Starts a poll created with `start_blocked: true`.
- **`POST /poll/{id}/end`**: Ends a poll early.
- **`POST /poll/{id}/voters`**: Adds a new voter.
  - Request Body: `user_id=some_user`
- **`DELETE /poll/{id}/voters/delete`**: Removes a voter.
  - Request Body: `user_id=some_user`
- **`POST /poll/{id}/options`**: Adds a new option.
  - Request Body: `option=new_option`
- **`DELETE /poll/{id}/options/delete`**: Removes an option.
  - Request Body: `option=some_option`
