//go:build matrixportal_m4xxx

package main

import (
	"machine"

	"github.com/aykevl/things/hub75"
)

var display = hub75.New(hub75.Config{
	Data:         machine.SPI9_SDO_PIN,
	Clock:        machine.SPI9_SCK_PIN,
	Latch:        machine.HUB75_LAT,
	OutputEnable: machine.HUB75_OE,
	A:            machine.HUB75_ADDR_A,
	B:            machine.HUB75_ADDR_B,
	C:            machine.HUB75_ADDR_C,
	D:            machine.HUB75_ADDR_D,
	Brightness:   0x9F,
	NumScreens:   4, // screens are 32x32 as far as this driver is concerned
})

func init() {
	machine.SPI9.Configure(machine.SPIConfig{
		SDI: machine.SPI9_SDI_PIN,
		SDO: machine.SPI9_SDO_PIN,
		SCK: machine.SPI9_SCK_PIN,
	})
	println("init")
}
