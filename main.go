package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/simonvetter/modbus"
)

// RegisterType represents the type of Modbus register
type RegisterType string

const (
	TypeCoil            RegisterType = "coil"
	TypeDiscreteInput   RegisterType = "discrete_input"
	TypeInputRegister   RegisterType = "input_register"
	TypeHoldingRegister RegisterType = "holding_register"
)

// RegisterConfig defines a register to poll
type RegisterConfig struct {
	Name     string       `json:"name"`
	Address  uint16       `json:"address"`
	Type     RegisterType `json:"type"`
	MinValue int          `json:"min_value"`
	MaxValue int          `json:"max_value"`
}

// HostConfig contains configuration for a single host
type HostConfig struct {
	Name      string           `json:"name"`
	Host      string           `json:"host"`
	Port      int              `json:"port"`
	UnitID    uint8            `json:"unit_id"`
	Timeout   string           `json:"timeout"`  // e.g., "5s", "1m"
	Interval  string           `json:"interval"` // polling interval
	Enabled   bool             `json:"enabled"`  // whether to poll this host
	Registers []RegisterConfig `json:"registers"`
}

// ClientConfig holds the entire client configuration
type ClientConfig struct {
	Hosts []HostConfig `json:"hosts"`
}

// ReadResult holds the result of a register read operation
type ReadResult struct {
	HostName string
	Register RegisterConfig
	Value    interface{}
	Error    error
	Success  bool
	Duration time.Duration
}

// HostClient manages connection and polling for a single host
type HostClient struct {
	config    HostConfig
	client    *modbus.ModbusClient
	results   []ReadResult
	mu        sync.Mutex
	connected bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

type MultiHostClient struct {
	config      *ClientConfig
	hostClients map[string]*HostClient
	results     chan ReadResult
	done        chan struct{}
}

func main() {
	// Parse command line flags
	configFile := flag.String("config", "config.json", "Path to JSON configuration file")
	cycles := flag.Int("cycles", 0, "Number of polling cycles (0 for infinite)")
	single := flag.Bool("single", false, "Run only once then exit")
	logFile := flag.String("log", "", "Log to file instead of stdout")
	flag.Parse()

	// Set up logging
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("Failed to open log file: %v", err)
		}
		defer f.Close()
		log.SetOutput(f)
	} else {
		log.SetOutput(os.Stdout)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Load configuration
	config, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create and start multi-host client
	multiClient := &MultiHostClient{
		config:      config,
		hostClients: make(map[string]*HostClient),
		results:     make(chan ReadResult, 1000),
		done:        make(chan struct{}),
	}

	// Initialize host clients
	for _, hostConfig := range config.Hosts {
		if !hostConfig.Enabled {
			log.Printf("Host %s is disabled, skipping", hostConfig.Name)
			continue
		}

		hostClient, err := multiClient.createHostClient(hostConfig)
		if err != nil {
			log.Printf("Failed to create client for host %s: %v", hostConfig.Name, err)
			continue
		}
		multiClient.hostClients[hostConfig.Name] = hostClient
	}

	if len(multiClient.hostClients) == 0 {
		log.Fatal("No enabled hosts found in configuration")
	}

	// Start result collector
	go multiClient.collectResults()

	// Start polling for each host
	for _, hostClient := range multiClient.hostClients {
		hostClient.ctx, hostClient.cancel = context.WithCancel(context.Background())
		// Pass the multiClient to each hostClient
		hostClient.ctx = context.WithValue(hostClient.ctx, "multiClient", multiClient)

		if *single {
			hostClient.wg.Add(1)
			go func(hc *HostClient) {
				defer hc.wg.Done()
				hc.pollOnce(multiClient) // Pass multiClient
			}(hostClient)
		} else {
			hostClient.wg.Add(1)
			go func(hc *HostClient) {
				defer hc.wg.Done()
				hc.pollLoop(*cycles, multiClient) // Pass multiClient
			}(hostClient)
		}
	}

	// Wait for all polling to complete
	multiClient.waitForCompletion()

	log.Println("Polling completed")
}

// loadConfig loads the client configuration from a JSON file
func loadConfig(filename string) (*ClientConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	var config ClientConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config: %v", err)
	}

	// Set defaults
	for i := range config.Hosts {
		host := &config.Hosts[i]
		if host.Timeout == "" {
			host.Timeout = "10s"
		}
		if host.Interval == "" {
			host.Interval = "1s"
		}
		if host.UnitID == 0 {
			host.UnitID = 1
		}
		if host.Port == 0 {
			host.Port = 502
		}
	}

	return &config, nil
}

// createHostClient creates a new client for a specific host
func (mc *MultiHostClient) createHostClient(config HostConfig) (*HostClient, error) {
	timeout, err := time.ParseDuration(config.Timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout format: %v", err)
	}

	url := fmt.Sprintf("tcp://%s:%d", config.Host, config.Port)
	clientConfig := &modbus.ClientConfiguration{
		URL:     url,
		Timeout: timeout,
	}

	client, err := modbus.NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating client: %v", err)
	}

	err = client.SetUnitId(config.UnitID)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("error setting unit ID: %v", err)
	}

	hostClient := &HostClient{
		config: config,
		client: client,
	}

	log.Printf("Created client for host %s (%s:%d)", config.Name, config.Host, config.Port)
	return hostClient, nil
}

// pollLoop performs continuous polling for a host
func (hc *HostClient) pollLoop(cycles int, mc *MultiHostClient) {
	interval, err := time.ParseDuration(hc.config.Interval)
	if err != nil {
		log.Printf("Host %s: Invalid interval format: %v", hc.config.Name, err)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case <-hc.ctx.Done():
			return
		case <-ticker.C:
			count++
			if cycles > 0 && count > cycles {
				return
			}
			hc.pollOnce(mc)
		}
	}
}

// pollOnce performs a single poll of all registers for a host
func (hc *HostClient) pollOnce(mc *MultiHostClient) {
	// Connect if not connected
	if !hc.connected {
		err := hc.client.Open()
		if err != nil {
			log.Printf("Host %s: Connection failed: %v", hc.config.Name, err)
			return
		}
		hc.connected = true
		log.Printf("Host %s: Connected successfully", hc.config.Name)
	}

	// Poll all registers
	for i, reg := range hc.config.Registers {
		// Add small delay between reads to prevent overwhelming the server
		if i > 0 {
			time.Sleep(50 * time.Millisecond)
		}

		result := hc.readRegister(reg)

		// If we get a connection error, mark as disconnected
		if result.Error != nil && strings.Contains(result.Error.Error(), "connection") {
			hc.connected = false
			log.Printf("Host %s: Connection lost, will reconnect", hc.config.Name)
			// Close the existing connection
			if hc.client != nil {
				hc.client.Close()
			}
		}

		hc.sendResult(result, mc)
	}
}

// readRegister reads a single register
func (hc *HostClient) readRegister(reg RegisterConfig) ReadResult {
	startTime := time.Now()
	result := ReadResult{
		HostName: hc.config.Name,
		Register: reg,
	}

	address := hc.getModbusAddress(reg)

	switch reg.Type {
	case TypeCoil:
		value, err := hc.client.ReadCoil(address)
		if err != nil {
			result.Error = err
		} else {
			result.Value = value
			result.Success = true
		}

	case TypeDiscreteInput:
		value, err := hc.client.ReadDiscreteInput(address)
		if err != nil {
			result.Error = err
		} else {
			result.Value = value
			result.Success = true
		}

	case TypeInputRegister:
		registers, err := hc.client.ReadRegisters(address, 1, modbus.INPUT_REGISTER)
		if err != nil {
			result.Error = err
		} else {
			if len(registers) > 0 {
				intValue := hc.interpretRegisterValue(registers[0], reg)
				result.Value = intValue
				result.Success = true
			} else {
				result.Error = fmt.Errorf("no data received")
			}
		}

	case TypeHoldingRegister:
		registers, err := hc.client.ReadRegisters(address, 1, modbus.HOLDING_REGISTER)
		if err != nil {
			result.Error = err
		} else {
			if len(registers) > 0 {
				intValue := hc.interpretRegisterValue(registers[0], reg)
				result.Value = intValue
				result.Success = true
			} else {
				result.Error = fmt.Errorf("no data received")
			}
		}

	default:
		result.Error = fmt.Errorf("unknown register type: %s", reg.Type)
	}

	result.Duration = time.Since(startTime)
	return result
}

// getModbusAddress converts config address to Modbus protocol address
func (hc *HostClient) getModbusAddress(reg RegisterConfig) uint16 {
	switch reg.Type {
	case TypeCoil:
		return reg.Address - 10000
	case TypeDiscreteInput:
		return reg.Address - 20000
	case TypeInputRegister:
		return reg.Address - 30000
	case TypeHoldingRegister:
		return reg.Address - 40000
	default:
		return reg.Address
	}
}

// interpretRegisterValue interprets the uint16 value as signed or unsigned
func (hc *HostClient) interpretRegisterValue(value uint16, reg RegisterConfig) int {
	if reg.MinValue < 0 || reg.MaxValue < 0 {
		if value > 32767 {
			return int(value) - 65536
		}
	}
	return int(value)
}

// sendResult sends a read result to the results channel
func (hc *HostClient) sendResult(result ReadResult, mc *MultiHostClient) {
	if mc != nil {
		select {
		case mc.results <- result:
		case <-hc.ctx.Done():
		}
	}
}

// collectResults collects and prints results from all hosts
func (mc *MultiHostClient) collectResults() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	resultBuffer := make(map[string][]ReadResult)
	lastDisplay := time.Now()

	for {
		select {
		case result := <-mc.results:
			resultBuffer[result.HostName] = append(resultBuffer[result.HostName], result)

			// Display results periodically
			if time.Since(lastDisplay) > 500*time.Millisecond && len(resultBuffer) > 0 {
				mc.displayResults(resultBuffer)
				resultBuffer = make(map[string][]ReadResult)
				lastDisplay = time.Now()
			}

		case <-mc.done:
			// Final display of any remaining results
			if len(resultBuffer) > 0 {
				mc.displayResults(resultBuffer)
			}
			return

		case <-ticker.C:
			// Periodic display if we have results but not enough activity
			if len(resultBuffer) > 0 && time.Since(lastDisplay) > 3*time.Second {
				mc.displayResults(resultBuffer)
				resultBuffer = make(map[string][]ReadResult)
				lastDisplay = time.Now()
			}
		}
	}
}

// displayResults displays collected results
func (mc *MultiHostClient) displayResults(resultBuffer map[string][]ReadResult) {
	fmt.Println("\n=== Polling Results ===")
	fmt.Printf("Timestamp: %s\n", time.Now().Format("2006-01-02 15:04:05.000"))
	fmt.Println("---------------------------------------------------------------")

	// Sort hosts for consistent display order
	var hosts []string
	for host := range resultBuffer {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)

	for _, host := range hosts {
		results := resultBuffer[host]
		if len(results) == 0 {
			continue
		}

		fmt.Printf("\nHost: %s\n", host)
		fmt.Printf("%-25s | %-15s | %-8s | %-7s | %s\n",
			"Register", "Type", "Value", "Time", "Status")
		fmt.Println("-----------------------------------------------------------------------------")

		for _, result := range results {
			status := "OK"
			valueStr := "N/A"
			timeStr := fmt.Sprintf("%4dms", result.Duration.Milliseconds())

			if result.Success {
				switch v := result.Value.(type) {
				case bool:
					valueStr = fmt.Sprintf("%v", v)
				case int:
					valueStr = fmt.Sprintf("%d", v)
					if result.Register.MinValue != 0 || result.Register.MaxValue != 0 {
						if v < result.Register.MinValue || v > result.Register.MaxValue {
							status = fmt.Sprintf("OUT OF RANGE [%d-%d]",
								result.Register.MinValue, result.Register.MaxValue)
						}
					}
				default:
					valueStr = fmt.Sprintf("%v", v)
				}
			} else {
				status = fmt.Sprintf("ERROR: %v", result.Error)
			}

			fmt.Printf("%-25s | %-15s | %-8s | %-7s | %s\n",
				result.Register.Name,
				result.Register.Type,
				valueStr,
				timeStr,
				status)
		}
	}
	fmt.Println("---------------------------------------------------------------")
}

// waitForCompletion waits for all host polling to complete
func (mc *MultiHostClient) waitForCompletion() {
	// Wait for all host clients to finish
	for _, hostClient := range mc.hostClients {
		hostClient.wg.Wait()
		if hostClient.connected {
			hostClient.client.Close()
		}
	}

	// Stop result collection
	close(mc.done)

	// Give a moment for final results to be processed
	time.Sleep(100 * time.Millisecond)
}
