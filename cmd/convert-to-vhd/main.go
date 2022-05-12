package main

import (
	"log"
	"os"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
)

func main() {
	f, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_RDONLY, 0777)
	if err != nil {
		log.Fatal(err)
	}
	if err := tar2ext4.ConvertToVhd(f); err != nil {
		log.Fatal(err)
	}
}
