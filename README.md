# CronChat Backend

🚀 **CronChat Backend** is a backend service that provides realtime chat functionality within a service-oriented system.

The service is designed as an independent component, with clear responsibilities around authentication, realtime communication, and chat-related data management. It is built with maintainability and extensibility in mind, allowing future integration with other services.

---

## Overview

CronChat Backend is responsible for:

- Authentication and authorization
- Room and membership management
- Realtime message delivery
- Message state handling (reactions, replies, read status)
- Media handling at the service level

The implementation focuses on clear structure, predictable behavior, and incremental improvement.

---

## Technology Stack

- **Language**: Go
- **HTTP Server**: net/http
- **Realtime Communication**: WebSocket
- **Database**: MySQL
- **Authentication**: JWT (access token)
- **Containerization**: Docker, Docker Compose

---

## Features

### Authentication
- JWT-based access token authentication
- HTTP middleware for request validation

### Chat
- Direct (1–1) and group chat rooms
- Realtime messaging via WebSocket
- Text and image messages
- Emoji reactions
- Message replies
- Basic read / unread tracking

### Room Management
- Create and manage chat rooms
- Add and remove members
- Realtime room updates (last message, unread count)

### User
- User profile management
- Avatar upload and retrieval
...
---

## Project Structure

```text
api-service/
├── cmd/
│   └── main.go
├── internal/
│   ├── chat/          # messaging, reactions, websocket handling
│   ├── room/          # room and membership logic
│   ├── user/          # user profile and avatar handling
│   ├── auth/          # authentication and middleware
│   └── httpserver/    # routing and HTTP handlers
├── data/              # local image storage (placeholder before introducing a dedicated media/storage service)
├── migrations/        # database schema and migrations
├── docker-compose.yml
├── .sql               # database
└── Dockerfile

