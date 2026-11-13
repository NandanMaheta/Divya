/**
 * Money is always carried as integer paise (BR-SET-07). These helpers only format for display;
 * every calculation happens in the Go settlement engine.
 */
export type Paise = number & { readonly __brand: "Paise" };

export function paise(value: number): Paise {
  if (!Number.isSafeInteger(value)) {
    throw new RangeError(`paise must be a safe integer, got ${value}`);
  }
  return value as Paise;
}

const rupeeFormat = new Intl.NumberFormat("en-IN", {
  style: "currency",
  currency: "INR",
  maximumFractionDigits: 0,
});

const rupeePaiseFormat = new Intl.NumberFormat("en-IN", {
  style: "currency",
  currency: "INR",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

/** Formats paise with Indian digit grouping, e.g. 8658000 -> "₹86,580". */
export function formatRupees(amount: Paise, opts: { showPaise?: boolean } = {}): string {
  const rupees = amount / 100;
  return (opts.showPaise ? rupeePaiseFormat : rupeeFormat).format(rupees);
}
