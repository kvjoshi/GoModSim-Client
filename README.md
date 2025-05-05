# Multi-Host Modbus Client

This client application simultaneously polls multiple Modbus hosts, each with their own register configurations and polling intervals.

## Features

- Connect to multiple Modbus hosts simultaneously
- Individual configuration per host (timeout, interval, registers)
- Parallel polling with unique settings for each host
- Detailed per-host result display with timing information
- Graceful error handling for connection issues
- Enable/disable individual hosts
- Comprehensive logging and result aggregation

## Building

To build the client:

```bash
go build -o modbus-client main.go
```

## Running

### Basic Usage

```bash
./modbus-client --config multi-host-config.json
```

### Command-line Options

- `--config`: Path to JSON configuration file (default: multi-host-config.json)
- `--cycles`: Number of polling cycles (default: 10, 0 for infinite)
- `--single`: Run only once then exit
- `--log`: Log to file instead of stdout

### Examples

```bash
# Poll multiple hosts continuously
./modbus-client --cycles 0

# Single poll and exit
./modbus-client --single

# Log to file
./modbus-client --log modbus-client.log

# Use custom config file
./modbus-client --config custom-config.json
```

## Configuration File

The client uses a JSON configuration file to define multiple hosts and their registers:

```json
{
  "hosts": [
    {
      "name": "Main Controller",
      "host": "localhost",
      "port": 502,
      "unit_id": 1,
      "timeout": "5s",
      "interval": "1s",
      "enabled": true,
      "registers": [
        {
          "name": "Emergency Stop",
          "address": 10001,
          "type": "coil",
          "min_value": 0,
          "max_value": 1
        }
      ]
    },
    {
      "name": "Sensor Panel",
      "host": "192.168.1.100",
      "port": 502,
      "unit_id": 1,
      "timeout": "3s",
      "interval": "500ms",
      "enabled": true,
      "registers": []
    }
  ]
}
```

### Host Configuration Fields

- `name`: Host identifier for display purposes
- `host`: IP address or hostname
- `port`: Modbus TCP port (default: 502)
- `unit_id`: Modbus unit ID/slave ID (default: 1)
- `timeout`: Connection timeout (e.g., "5s", "10s")
- `interval`: Polling interval (e.g., "1s", "500ms")
- `enabled`: Whether to poll this host
- `registers`: Array of registers to poll for this host

### Register Configuration Fields

- `name`: Human-readable name for the register
- `address`: Modbus address (e.g., 10001 for coil 1)
- `type`: One of `coil`, `discrete_input`, `input_register`, or `holding_register`
- `min_value`: Minimum expected value (optional, for validation)
- `max_value`: Maximum expected value (optional, for validation)

## Output

The client displays results grouped by host:

```
=== Polling Results ===
Timestamp: 2024-05-05 14:30:00.000
---------------------------------------------------------------

Host: Main Controller
Register                  | Type            | Value    | Time    | Status
-----------------------------------------------------------------------------
Emergency Stop           | coil            | true     |   45ms  | OK
Humidity                 | input_register  | 65       |   60ms  | OK
Temperature              | input_register  | 25       |   50ms  | OK

Host: Sensor Panel
Register                  | Type            | Value    | Time    | Status
-----------------------------------------------------------------------------
X Axis Velocity          | input_register  | 1        |   78ms  | OK
Y Axis Velocity          | input_register  | 2        |   65ms  | OK
Vibration Intensity      | holding_register| 150      |   90ms  | OUT OF RANGE [0-700]

Host: Power Monitor
Register                  | Type            | Value    | Time    | Status
-----------------------------------------------------------------------------
Voltage                  | input_register  | 220      |  120ms  | OK
Current                  | input_register  | 1        |  110ms  | OK
---------------------------------------------------------------
```

## Advanced Features

### Parallel Polling
Each host runs in its own goroutine with independent polling intervals. A fast-polling sensor panel won't slow down a slower power monitor.

### Error Handling
The client handles:
- Connection failures (retries automatically)
- Communication timeouts
- Invalid responses
- Network interruptions

### Resource Management
- Proper connection cleanup on exit
- Context-based cancellation
- Graceful shutdown handling

## Performance Considerations

For optimal performance with many hosts:
- Adjust individual timeouts based on network conditions
- Balance polling intervals to avoid overloading the network
- Use the `enabled` flag to disable unused hosts
- Consider network bandwidth when setting very fast polling intervals

## License

MIT