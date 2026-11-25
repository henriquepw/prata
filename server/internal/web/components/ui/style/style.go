// Package style holds the console's shared interaction classes.
package style

// Inverted is the selection inversion of rows, links and options: foreground and background swap on hover, in --snap.
const Inverted = "transition-colors hover:bg-foreground hover:text-background"

// TextAction is a text action ([tema], [sair], [esc ✕]): muted caps that invert on hover, widened to a 44px touch
// target below @2xl in the console and below sm in the sheet, which sits outside the console container.
const TextAction = "cursor-pointer px-0.5 tracking-wider whitespace-nowrap text-muted-foreground uppercase transition-colors hover:bg-foreground hover:text-background @max-2xl/console:inline-flex @max-2xl/console:min-h-11 @max-2xl/console:min-w-11 @max-2xl/console:items-center @max-2xl/console:justify-center in-[.sheet]:max-sm:inline-flex in-[.sheet]:max-sm:min-h-11 in-[.sheet]:max-sm:items-center"
