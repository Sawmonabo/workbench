// Package display writes the forms a person reads in Workbench's output and
// messages: paths named from the home folder, times on the machine's clock,
// 12-hour with AM or PM, durations in their two largest units (the smaller
// left out when it is zero), counts with their nouns, and byte sizes. Sizes
// follow macOS: memory, and the text and files the guard keeps, in binary
// units, as Activity Monitor counts memory (a MB is 1,024 KB of 1,024
// bytes); downloads in decimal units, as Finder shows file sizes (a MB is
// 1,000 kB of 1,000 bytes). Each form has one rule here, so every command,
// view and message writes it alike. It imports nothing from Workbench.
package display
