package test

import (
	"log"
	"time"

	"github.com/simonvetter/modbus"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Test coil read first
	log.Println("Testing coil read...")
	client, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:     "tcp://127.0.0.1:502",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = client.Open()
	if err != nil {
		log.Fatal(err)
	}

	err = client.SetUnitId(1)
	if err != nil {
		log.Fatal(err)
	}

	// Read coil first (this works)
	coil, err := client.ReadCoil(1)
	if err != nil {
		log.Printf("Coil read failed: %v", err)
	} else {
		log.Printf("Coil read success: %v", coil)
	}

	// Sleep a bit
	time.Sleep(1 * time.Second)

	// Read input register (this fails)
	log.Println("Testing input register read...")
	register, err := client.ReadRegister(0, modbus.INPUT_REGISTER)
	if err != nil {
		log.Printf("Register read failed: %v", err)
	} else {
		log.Printf("Register read success: %v", register)
	}

	client.Close()
}
