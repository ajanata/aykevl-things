//go:build atsamd51

package hub75

import (
	"device/sam"
	"machine"
	"runtime/interrupt"
	"runtime/volatile"
	"unsafe"
)

type DeviceConfig struct {
	// Bus is the SPI bus to use.
	Bus *machine.SPI
	// TriggerSource is the DMA trigger source, e.g. SERCOM4_DMAC_ID_TX = 0x0D. This must be for the sercom used for
	// Bus.
	TriggerSource uint32
	// OETimerCounterControl is the TCC for the OE pin.
	OETimerCounterControl *sam.TCC_Type
	// TimerChannel is the channel of the TCC that the OE pin is on.
	TimerChannel int8
	// TimerIntenset is the interrupt enable bit value of the timer channel that OE is on, e.g. sam.TCC_INTENSET_MC0.
	// This must match TimerChannel.
	TimerIntenset uint32
	// DMAChannel is the DMA channel to use.
	DMAChannel uint8
	// DMADescriptor is the descriptor for the specified DMA channel.
	DMADescriptor *DmaDescriptor
}

type chipSpecificSettings struct {
	bus           *machine.SPI
	dmaChannel    uint8
	timer         *sam.TCC_Type
	timerChannel  *volatile.Register32
	timerIntenset uint32
	DmaDescriptor *DmaDescriptor
}

type DmaDescriptor struct {
	Btctrl   uint16
	Btcnt    uint16
	Srcaddr  unsafe.Pointer
	Dstaddr  unsafe.Pointer
	Descaddr unsafe.Pointer
}

func (d *Device) configureChip(_, _ machine.Pin, config DeviceConfig) {
	d.dmaChannel = config.DMAChannel
	d.DmaDescriptor = config.DMADescriptor
	d.bus = config.Bus

	// Configure channel descriptor.
	*config.DMADescriptor = DmaDescriptor{
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
	sam.DMAC.CHANNEL[d.dmaChannel].CHCTRLA.Set((sam.DMAC_CHANNEL_CHCTRLA_TRIGACT_BURST << sam.DMAC_CHANNEL_CHCTRLA_TRIGACT_Pos) | (config.TriggerSource << sam.DMAC_CHANNEL_CHCTRLA_TRIGSRC_Pos) | (sam.DMAC_CHANNEL_CHCTRLA_BURSTLEN_SINGLE << sam.DMAC_CHANNEL_CHCTRLA_BURSTLEN_Pos))

	// Enable SPI TXC interrupt.
	// Note that we're waiting for the TXC interrupt instead of the DMA complete
	// interrupt, because the DMA complete interrupt triggers before all data
	// has been shifted out completely (but presumably after the DMAC has sent
	// the last byte to the SPI peripheral).
	d.bus.Bus.INTENSET.Set(sam.SERCOM_SPIM_INTENSET_TXC)

	// Next up, configure the timer/counter used for precisely timing the Output
	// Enable pin.
	pwm := (*machine.TCC)(config.OETimerCounterControl)
	pwm.Configure(machine.PWMConfig{})
	pwm.Channel(d.oe)
	d.timer = config.OETimerCounterControl
	d.timerChannel = &d.timer.CC[config.TimerChannel]
	d.timerIntenset = config.TimerIntenset

	// Enable an interrupt on CC2 match.
	d.timer.INTENSET.Set(config.TimerIntenset)

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
	d.DmaDescriptor.Srcaddr = unsafe.Pointer(uintptr(unsafe.Pointer(&bitstring[0])) + uintptr(len(bitstring)))
	d.DmaDescriptor.Btcnt = uint16(len(bitstring)) // beat count

	// Start the transfer.
	sam.DMAC.CHANNEL[d.dmaChannel].CHCTRLA.SetBits(sam.DMAC_CHANNEL_CHCTRLA_ENABLE)
}

// startOutputEnableTimer will enable and disable the screen for a very short
// time, depending on which bit is currently enabled.
func (d *Device) startOutputEnableTimer() {
	// Multiplying the brightness by 3 to be consistent with the nrf52 driver
	// (48MHz vs 16MHz).
	count := (d.brightness * 3) << d.colorBit
	d.timerChannel.Set(0xffff - count)
	for d.timer.SYNCBUSY.HasBits(sam.TCC_SYNCBUSY_CC0 | sam.TCC_SYNCBUSY_CC1 | sam.TCC_SYNCBUSY_CC2 | sam.TCC_SYNCBUSY_CC3) {
	}
	d.timer.CTRLBSET.Set(sam.TCC_CTRLBSET_CMD_RETRIGGER << sam.TCC_CTRLBSET_CMD_Pos)
}

// SPIHandler is the SPI interrupt handler. You must call
//
//	interrupt.New(sam.IRQ_SERCOM4_1, hub75.SPIHandler)
//
// from your code (using the appropriate interrupt number). This must be done by you and not here because the interrupt
// number is required to be a constant by the compiler. Also ensure to enable that interrupt.
func SPIHandler(_ interrupt.Interrupt) {
	// Clear the interrupt flag.
	display.bus.Bus.INTFLAG.Set(sam.SERCOM_SPIM_INTFLAG_TXC)

	display.handleSPIEvent()
}

// TimerHandler is the timer interrupt handler. You must call
//
//	interrupt.New(sam.IRQ_TCC3_MC0, hub75.TimerHandler)
//
// from your code (using the appropriate interrupt number). This must be done by you and not here because the interrupt
// number is required to be a constant by the compiler. Also ensure to enable that interrupt and possibly set its
// priority.
func TimerHandler(_ interrupt.Interrupt) {
	// Clear the interrupt flag.
	// TODO this is assuming that all relevant interrupt flag values match the intenset values. That may not always be
	// the case. Regardless, this was originally sam.TCC_INTFLAG_MC0.
	display.chipSpecificSettings.timer.INTFLAG.Set(display.chipSpecificSettings.timerIntenset)

	display.handleTimerEvent()
}
