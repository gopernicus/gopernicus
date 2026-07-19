import { test, expect } from "@playwright/test";

// GOTH-3.3 compact-selection primitives: real-browser interaction proof for Input
// OTP (P30), Toggle (P35), and Toggle Group (P36) across all three engines. Input
// OTP and Toggle are native (StylesOnly, no controller) so their tests double as a
// no-JavaScript baseline proof; Toggle Group's multiple mode is enhanced by the
// frozen gothRovingFocus controller (Interactive profile). The compact-form test
// proves ordinary no-JS form submission of the native values.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Input OTP (P30)", () => {
  test("native single-char slots: typing, maxlength, tab, backspace, no echo", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-input-otp");

    const s1 = page.locator("#otp-1");
    const s2 = page.locator("#otp-2");

    // The first slot offers a one-time-code and each slot holds one character.
    await expect(s1).toHaveAttribute("autocomplete", "one-time-code");
    await expect(s1).toHaveAttribute("maxlength", "1");
    await expect(s1).toHaveAttribute("inputmode", "numeric");

    // Native typing respects maxlength (a second key is rejected, no auto-advance).
    await s1.focus();
    await page.keyboard.type("12");
    await expect(s1).toHaveJSProperty("value", "1");

    // Native Tab moves to the next slot (the separator is skipped natively).
    await page.keyboard.press("Tab");
    await expect(s2).toBeFocused();
    await page.keyboard.type("3");
    await expect(s2).toHaveJSProperty("value", "3");

    // Native Backspace clears within a slot.
    await page.keyboard.press("Backspace");
    await expect(s2).toHaveJSProperty("value", "");

    // No secret echo: the invalid group shows aria-invalid slots with no digits and
    // a generic (digit-free) error message.
    const bad1 = page.locator("#otp-bad-1");
    await expect(bad1).toHaveAttribute("aria-invalid", "true");
    await expect(bad1).toHaveJSProperty("value", "");
    await expect(page.locator("#otp-bad-err")).not.toHaveText(/\d/);

    expect(errors).toEqual([]);
  });
});

test.describe("Toggle (P35)", () => {
  test("checkbox-backed toggle presses with pointer and Space, no JS", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-toggle");

    const bold = page.locator("#tg-bold .goth-toggle-input");
    await expect(bold).not.toBeChecked();

    // Clicking the styled label (the button) toggles the wrapped checkbox natively.
    await page.locator("#tg-bold").click();
    await expect(bold).toBeChecked();

    // Space toggles the focused control natively.
    await bold.focus();
    await page.keyboard.press(" ");
    await expect(bold).not.toBeChecked();

    // A server-pressed toggle is checked; a disabled one cannot toggle.
    await expect(page.locator("#tg-italic .goth-toggle-input")).toBeChecked();
    await expect(page.locator("#tg-off .goth-toggle-input")).toBeDisabled();

    expect(errors).toEqual([]);
  });
});

test.describe("Toggle Group (P36)", () => {
  test("single = native radios; multiple = roving-focus checkboxes", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-toggle-group");
    await page.waitForFunction(() => "Alpine" in window);

    // Single mode: native radios sharing one name — one selected, arrow moves it.
    const left = page.locator("#tga-left .goth-toggle-group-input");
    const center = page.locator("#tga-center .goth-toggle-group-input");
    await expect(left).toBeChecked();
    await left.focus();
    await page.keyboard.press("ArrowRight");
    await expect(center).toBeChecked();
    await expect(left).not.toBeChecked();

    // Multiple mode: gothRovingFocus gives one tab stop; Space toggles; multi-select.
    const mb = page.locator("#tgf-bold .goth-toggle-group-input");
    const mi = page.locator("#tgf-italic .goth-toggle-group-input");
    await expect(mb).toBeChecked();
    await expect(mb).toHaveAttribute("tabindex", "0");
    await expect(mi).toHaveAttribute("tabindex", "-1");

    await mb.focus();
    await page.keyboard.press("ArrowRight");
    await expect(mi).toBeFocused();
    await page.keyboard.press(" ");
    await expect(mi).toBeChecked();
    // Multiple selection: bold stays pressed alongside italic.
    await expect(mb).toBeChecked();

    expect(errors).toEqual([]);
  });
});

test.describe("Compact form (no-JS submission)", () => {
  test("toggle, toggle group, and OTP submit their values with no JavaScript", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/compact-form");

    // Press the bold toggle and pick center alignment.
    await page.locator("#cf-bold").click();
    await page.locator("#cf-center").click();

    // Fill the six OTP slots.
    for (let i = 1; i <= 6; i++) {
      await page.locator(`#cf-otp-${i}`).fill(String(i));
    }

    // Submitting navigates natively (GET) to the echo route.
    await Promise.all([
      page.waitForURL(/\/compact\/echo/),
      page.locator('[data-slot="compact-form"] button[type="submit"]').click(),
    ]);

    await expect(page.locator('[data-field="bold"]')).toHaveText("on");
    await expect(page.locator('[data-field="align"]')).toHaveText("center");
    // The OTP is reported by count only (no secret echo of the digits).
    await expect(page.locator('[data-field="otp-count"]')).toHaveText("6");

    expect(errors).toEqual([]);
  });
});
