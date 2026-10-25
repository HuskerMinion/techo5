// Package tessera makes the device a screen for Tessera, a Home Assistant add-on that lays out pages of
// tiles in an editor and sends them to the screens it finds. Tessera's own screens are ESP32 panels running
// its firmware; this speaks the same protocol from the other end, so the add-on lists the device beside them
// and sends it layouts and tile states through Home Assistant.
//
// What is here is the protocol, the entities the add-on looks for, and the layout as the screen draws it
// (View) with what a tap on a tile asks of Home Assistant. The layout is kept, in memory and on disk; the
// display draws it as the dashboard page while the Dashboard setting is Tessera. Off unless somebody turns
// it on, and only on the Show: the Dot has no screen, and the Spot's round one wants a layout of its own.
package tessera
