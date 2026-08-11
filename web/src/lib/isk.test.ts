import { describe, expect, it } from "vitest";

import { formatIsk, InvalidIskValueError, isValidIsk } from "./isk";

describe("formatIsk", () => {
  it("groups thousands and pads two decimal places by default", () => {
    expect(formatIsk("1234567")).toBe("1,234,567.00");
  });

  it("preserves an existing decimal part", () => {
    expect(formatIsk("1234567.5")).toBe("1,234,567.50");
  });

  it("truncates a longer decimal part rather than rounding", () => {
    expect(formatIsk("1234567.999")).toBe("1,234,567.99");
  });

  it("handles negative values", () => {
    expect(formatIsk("-1234567.89")).toBe("-1,234,567.89");
  });

  it("handles values far beyond Number.MAX_SAFE_INTEGER without precision loss", () => {
    const huge = "123456789012345678901234567890";
    expect(formatIsk(huge, { fractionDigits: 0 })).toBe(
      "123,456,789,012,345,678,901,234,567,890",
    );
  });

  it("throws InvalidIskValueError on a malformed wire value", () => {
    expect(() => formatIsk("1.2.3")).toThrow(InvalidIskValueError);
    expect(() => formatIsk("NaN")).toThrow(InvalidIskValueError);
    expect(() => formatIsk("")).toThrow(InvalidIskValueError);
  });
});

describe("isValidIsk", () => {
  it("accepts optionally-signed decimal strings", () => {
    expect(isValidIsk("0")).toBe(true);
    expect(isValidIsk("-42")).toBe(true);
    expect(isValidIsk("42.50")).toBe(true);
  });

  it("rejects non-numeric strings", () => {
    expect(isValidIsk("abc")).toBe(false);
    expect(isValidIsk("1e10")).toBe(false);
  });
});
