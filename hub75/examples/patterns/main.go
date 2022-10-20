package main

import (
	"image/color"
	"machine"
	"time"

	"github.com/aykevl/ledsgo"

	"github.com/aykevl/things/hub75"
)

const (
	brightness = 0xff
	spread     = 6  // higher means the noise gets more detailed
	speed      = 20 // higher means slower
)

var display *hub75.Device

func main() {
	time.Sleep(time.Second)

	machine.SPI9.Configure(machine.SPIConfig{
		SDI:       machine.SPI9_SDI_PIN,
		SDO:       machine.SPI9_SDO_PIN,
		SCK:       machine.SPI9_SCK_PIN,
		Frequency: 16 * machine.MHz,
	})
	println("main init")
	display = hub75.New(hub75.Config{
		Data:         machine.SPI9_SDO_PIN,
		Clock:        machine.SPI9_SCK_PIN,
		Latch:        machine.HUB75_LAT,
		OutputEnable: machine.HUB75_OE,
		A:            machine.HUB75_ADDR_A,
		B:            machine.HUB75_ADDR_B,
		C:            machine.HUB75_ADDR_C,
		D:            machine.HUB75_ADDR_D,
		Brightness:   0xFF,
		NumScreens:   4, // screens are 32x32 as far as this driver is concerned
	})
	println("hub75 init")

	fullRefreshes := uint(0)
	previousSecond := int64(0)
	for {
		start := time.Now()
		noise(start)
		// fire()
		display.Display()

		second := (start.UnixNano() / int64(time.Second))
		if second != previousSecond {
			previousSecond = second
			newFullRefreshes := display.FullRefreshes()
			print("#", second, " screen=", newFullRefreshes-fullRefreshes, "fps animation=", time.Since(start).String(), "\r\n")
			fullRefreshes = newFullRefreshes
		}
	}
}

func noise(now time.Time) {
	for x := int16(0); x < 128; x++ {
		for y := int16(0); y < 32; y++ {
			hue := uint16(ledsgo.Noise3(uint32(now.UnixNano()>>speed), uint32(x<<spread), uint32(y<<spread))) * 2
			display.SetPixel(x, y, ledsgo.Color{hue, 0xff, brightness}.Spectrum())
		}
	}
}

func fire() {
	const pointsPerCircle = 12 // how many LEDs there are per turn of the torch
	const cooling = 8          // higher means faster cooling
	const detail = 400         // higher means more detailed flames
	const speed = 12           // higher means faster
	now := time.Now().UnixNano()
	for x := int16(0); x < 128; x++ {
		for y := int16(0); y < 32; y++ {
			heat := ledsgo.Noise2(uint32(y*detail)-uint32((now>>20)*speed), uint32(x*detail))/256 + 128
			heat -= uint16(y) * cooling
			if heat < 0 {
				heat = 0
			}
			display.SetPixel(x, y, heatMap(uint8(heat)))
		}
	}
}

func heatMap(index uint8) color.RGBA {
	if index < 128 {
		return color.RGBA{index * 2, 0, 0, 255}
	}
	if index < 224 {
		return color.RGBA{255, uint8(uint32(index-128) * 8 / 3), 0, 255}
	}
	return color.RGBA{255, 255, (index - 224) * 8, 255}
}
