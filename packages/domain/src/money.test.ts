import { describe, expect, it } from "vitest";
import { formatRupees, paise } from "./money";

describe("formatRupees", () => {
  it.each([
    [8658000, "₹86,580"], // GS-01 total
    [150000, "₹1,500"], // GS-02
    [1150000, "₹11,500"], // GS-03
    [14850000, "₹1,48,500"],
    [0, "₹0"],
  ])("formats %i paise as %s", (value, expected) => {
    expect(formatRupees(paise(value))).toBe(expected);
  });

  it("can show paise", () => {
    expect(formatRupees(paise(1234567), { showPaise: true })).toBe("₹12,345.67");
  });

  it("formats negative amounts", () => {
    expect(formatRupees(paise(-50000))).toBe("-₹500");
  });

  it("rejects non-integer paise", () => {
    expect(() => paise(10.5)).toThrow(RangeError);
  });
});
