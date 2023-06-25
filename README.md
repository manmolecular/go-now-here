# go-now-here
WebSocket chat with a random person.

## Screenshot
User interface:
![image](assets/main_page.png)

## Description
This project is a simple WebSocket-based chat where your messages are sent only to one free client from the pool of available connections. Simply put, it is a chat with a randomly assigned person.

There are no extra configuration possibilities, no fancy UI, no security measures, no tests, no HTTPS, etc. - just a rough implementation of the idea.

Thanks for attention.

## Requirements
One of:
- Go (tested on 1.20) to build and run natively
- Docker (tested on 20.10) and Docker Compose (tested on 2.17) to run in docker

## Run
Perform one of the following commands:
```
make        # build with Docker, run and attach to logs
make build  # to build a Docker image
make run    # to run the application using "docker compose"
make stop   # to stop the appllication
make delete # to delete a container with the application
```
In your browser:
```
http://localhost:8000/
```
