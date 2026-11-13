import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TodayPage } from "./index";

describe("TodayPage", () => {
  it("renders the heading and Indian-formatted money", () => {
    render(<TodayPage />);
    expect(screen.getByRole("heading", { name: "Today" })).toBeDefined();
    expect(screen.getByText(/₹86,580/)).toBeDefined();
  });
});
