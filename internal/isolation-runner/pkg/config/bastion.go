package config

import "os"

func GetBastionAddress() string {
	address := os.Getenv("BASTION_ADDRESS")
	if address == "" {
		address = "localhost:50054"
	}
	return address
}
