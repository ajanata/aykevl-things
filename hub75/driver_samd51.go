//go:build atsamd51
// +build atsamd51

package hub75

import (
	"unsafe"

	"device/arm"
	"device/sam"
	"machine"
	"runtime/volatile"
)

const dmaDescriptors = 2

//go:align 16
var DmaDescriptorSection [dmaDescriptors]DmaDescriptor

//go:align 16
var DmaDescriptorWritebackSection [dmaDescriptors]DmaDescriptor

type chipSpecificSettings struct {
	bus          *machine.SPI
	dmaChannel   uint8
	timer        *sam.TCC_Type
	timerChannel *volatile.Register32
}

type DmaDescriptor struct {
	Btctrl   uint16
	Btcnt    uint16
	Srcaddr  unsafe.Pointer
	Dstaddr  unsafe.Pointer
	Descaddr unsafe.Pointer
}

func (d *Device) configureChip(dataPin, clockPin machine.Pin) {
	d.dmaChannel = 0
	d.bus = &machine.SPI9 // must be SERCOM1
	// const triggerSource = 0x07 // SERCOM1_DMAC_ID_TX
	const triggerSource = 0x0D // SERCOM4_DMAC_ID_TX
	// d.bus.Configure(machine.SPIConfig{
	// 	Frequency: 16000000,
	// 	Mode:      0,
	// })

	// Init DMAC.
	// First configure the clocks, then configure the DMA descriptors. Those
	// descriptors must live in SRAM and must be aligned on a 16-byte boundary.
	// http://www.lucadavidian.com/2018/03/08/wifi-controlled-neo-pixels-strips/
	// https://svn.larosterna.com/oss/trunk/arduino/zerotimer/zerodma.cpp
	sam.MCLK.AHBMASK.SetBits(sam.MCLK_AHBMASK_DMAC_)
	sam.DMAC.BASEADDR.Set(uint32(uintptr(unsafe.Pointer(&DmaDescriptorSection))))
	sam.DMAC.WRBADDR.Set(uint32(uintptr(unsafe.Pointer(&DmaDescriptorWritebackSection))))

	// Enable peripheral with all priorities.
	sam.DMAC.CTRL.SetBits(sam.DMAC_CTRL_DMAENABLE | sam.DMAC_CTRL_LVLEN0 | sam.DMAC_CTRL_LVLEN1 | sam.DMAC_CTRL_LVLEN2 | sam.DMAC_CTRL_LVLEN3)

	// Configure channel descriptor.
	DmaDescriptorSection[d.dmaChannel] = DmaDescriptor{
		Btctrl: (1 << 0) | // VALID: Descriptor Valid
			(0 << 3) | // BLOCKACT=NOACT: Block Action
			(1 << 10) | // SRCINC: Source Address Increment Enable
			(0 << 11) | // DSTINC: Destination Address Increment Enable
			(1 << 12) | // STEPSEL=SRC: Step Selection
			(0 << 13), // STEPSIZE=X1: Address Increment Step Size
		Dstaddr: unsafe.Pointer(&d.bus.Bus.DATA.Reg),
	}

	// Reset channel.
	sam.DMAC.CHANNEL[d.dmaChannel].CHCTRLA.ClearBits(sam.DMAC_CHANNEL_CHCTRLA_ENABLE)
	sam.DMAC.CHANNEL[d.dmaChannel].CHCTRLA.SetBits(sam.DMAC_CHANNEL_CHCTRLA_SWRST)

	// Configure channel.
	sam.DMAC.CHANNEL[d.dmaChannel].CHPRILVL.Set(0)
	sam.DMAC.CHANNEL[d.dmaChannel].CHCTRLA.Set((sam.DMAC_CHANNEL_CHCTRLA_TRIGACT_BURST << sam.DMAC_CHANNEL_CHCTRLA_TRIGACT_Pos) | (triggerSource << sam.DMAC_CHANNEL_CHCTRLA_TRIGSRC_Pos) | (sam.DMAC_CHANNEL_CHCTRLA_BURSTLEN_SINGLE << sam.DMAC_CHANNEL_CHCTRLA_BURSTLEN_Pos))

	// Enable SPI TXC interrupt.
	// Note that we're waiting for the TXC interrupt instead of the DMA complete
	// interrupt, because the DMA complete interrupt triggers before all data
	// has been shifted out completely (but presumably after the DMAC has sent
	// the last byte to the SPI peripheral).
	d.bus.Bus.INTENSET.Set(sam.SERCOM_SPIM_INTENSET_TXC)
	// arm.EnableIRQ(sam.IRQ_SERCOM1_1)
	arm.EnableIRQ(sam.IRQ_SERCOM4_1)

	// Next up, configure the timer/counter used for precisely timing the Output
	// Enable pin.
	// d.oe == PB12
	// PA18 is on TCC1 WO[2]
	pwm := machine.TCC3
	pwm.Configure(machine.PWMConfig{})
	pwm.Channel(d.oe)
	d.timer = sam.TCC3
	d.timerChannel = &d.timer.CC[0]

	// Enable an interrupt on CC2 match.
	d.timer.INTENSET.Set(sam.TCC_INTENSET_MC0)
	arm.EnableIRQ(sam.IRQ_TCC3_MC0)

	// Set to one-shot and count down.
	d.timer.CTRLBSET.SetBits(sam.TCC_CTRLBSET_ONESHOT | sam.TCC_CTRLBSET_DIR)
	for d.timer.SYNCBUSY.HasBits(sam.TCC_SYNCBUSY_CTRLB) {
	}

	// Enable TCC output.
	d.timer.CTRLA.SetBits(sam.TCC_CTRLA_ENABLE)
	for d.timer.SYNCBUSY.HasBits(sam.TCC_SYNCBUSY_ENABLE) {
	}
}

// startTransfer starts the SPI transaction to send the next row to the screen.
func (d *Device) startTransfer() {
	bitstring := d.displayBitstrings[d.row][d.colorBit]

	// For some reason, you have to provide the address just past the end of the
	// array instead of the address of the array.
	descriptor := &DmaDescriptorSection[d.dmaChannel]
	descriptor.Srcaddr = unsafe.Pointer(uintptr(unsafe.Pointer(&bitstring[0])) + uintptr(len(bitstring)))
	// descriptor.Srcaddr = unsafe.Pointer(uintptr(unsafe.Pointer(&bitstring[0])))
	descriptor.Btcnt = uint16(len(bitstring)) // beat count

	// Start the transfer.
	sam.DMAC.CHANNEL[d.dmaChannel].CHCTRLA.SetBits(sam.DMAC_CHANNEL_CHCTRLA_ENABLE)
}

// startOutputEnableTimer will enable and disable the screen for a very short
// time, depending on which bit is currently enabled.
func (d *Device) startOutputEnableTimer() {
	// Multiplying the brightness by 3 to be consistent with the nrf52 driver
	// (48MHz vs 16MHz).
	// Except using 3 here causes an underflow for the timer duration on near-max brightness
	count := (d.brightness * 3) << d.colorBit
	// count := d.brightness << d.colorBit
	d.timerChannel.Set(0xffff - count)
	for d.timer.SYNCBUSY.HasBits(sam.TCC_SYNCBUSY_CC0 | sam.TCC_SYNCBUSY_CC1 | sam.TCC_SYNCBUSY_CC2 | sam.TCC_SYNCBUSY_CC3) {
	}
	d.timer.CTRLBSET.Set(sam.TCC_CTRLBSET_CMD_RETRIGGER << sam.TCC_CTRLBSET_CMD_Pos)
}

// // SPI TXC interrupt is on interrupt line 1.
// //
// //export SERCOM1_1_IRQHandler
// func spiHandler() {
// 	// Clear the interrupt flag.
// 	display.bus.Bus.INTFLAG.Set(sam.SERCOM_SPIM_INTFLAG_TXC)
//
// 	display.handleSPIEvent()
// }
//
// //export TCC3_MC0_IRQHandler
// func tcc1Handler() {
// 	// Clear the interrupt flag.
// 	sam.TCC3.INTFLAG.Set(sam.TCC_INTFLAG_MC0)
//
// 	display.handleTimerEvent()
// }

// SPI TXC interrupt is on interrupt line 1.
//
//export SERCOM4_1_IRQHandler
func spiHandler() {
	// Clear the interrupt flag.
	display.bus.Bus.INTFLAG.Set(sam.SERCOM_SPIM_INTFLAG_TXC)

	display.handleSPIEvent()
}

//export TCC3_MC0_IRQHandler
func tcc1Handler() {
	// Clear the interrupt flag.
	sam.TCC3.INTFLAG.Set(sam.TCC_INTFLAG_MC0)

	display.handleTimerEvent()
}
