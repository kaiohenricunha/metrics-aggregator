# Metrics Aggregator

This microservice aggregates metrics from multiple containers and exposes them on a single endpoint and can be useful as a sidecar container in multi-container pods, each exposing its own set of metrics.

## Running Locally

### Prerequisites

- Docker
- Docker Compose

### Steps

1. Clone the repository:
    ```sh
    git clone git@github.com:kaiohenricunha/metrics-aggregator.git
    cd metrics-aggregator
    ```

2. Build and start the services using Docker Compose:
    ```sh
    docker-compose up --build
    ```

3. Access the aggregated metrics at:
    ```
    http://localhost:8080/metrics
    ```

### Configuration

The `docker-compose.yml` file defines the services and their configurations. The `METRICS_ENDPOINTS` environment variable is used to specify the endpoints from which metrics are aggregated.

### Simulating Real Metrics

The setup includes two Prometheus instances (`prometheus1` and `prometheus2`) that expose metrics. The configuration files [prometheus1.yml](http://_vscodecontentref_/1) and [prometheus2.yml](http://_vscodecontentref_/2) define the scrape configurations for these instances.

### Stopping the Services

To stop the services, run:

```sh
docker-compose down
```

### Logs

To view the logs of the aggregator service, run:

```sh
docker-compose logs -f aggregator
```
