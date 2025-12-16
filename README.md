# ConChat Backend

🚀 **ConChat Backend** is a backend service that provides realtime chat functionality within a larger, service-oriented system.

The project focuses on building a maintainable and extensible backend, applying common backend patterns such as authentication, realtime communication, data modeling, and service isolation. The chat service is designed as an independent component and can be extended or integrated with other services in the future.

---

## Overview

This service handles core chat-related responsibilities, including:

- User authentication and authorization
- Room and membership management
- Realtime message delivery
- Message state handling (reactions, replies, read status)
- Media handling at service level

The implementation emphasizes clear structure, incremental development, and practical backend considerations.

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
├── docker-compose.yml
└── Dockerfile
