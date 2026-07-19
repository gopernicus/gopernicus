import { test, expect } from "@playwright/test";

// GOTH-5.2 command/combobox primitives (P50 Combobox, P51 Command): real-browser
// proof across all three engines. The server owns the option data and, in the async
// specimen, the filtering and empty-state markup. These specs prove the no-JS
// baseline, the client and HTMX-fed filtering, the aria-activedescendant keyboard
// loop, and the form round-trip.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Combobox (P50)", () => {
  test("client filter: focus opens the listbox, typing narrows options", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-combobox");
    await page.waitForFunction(() => "Alpine" in window);

    const input = page.locator("#cb-input");
    await expect(input).toHaveAttribute("role", "combobox");
    await expect(input).toHaveAttribute("aria-expanded", "false");

    // The listbox is closed until the input is focused.
    await expect(page.locator("#cb-listbox")).toBeHidden();
    await input.focus();
    await expect(page.locator("#cb-listbox")).toBeVisible();
    await expect(input).toHaveAttribute("aria-expanded", "true");

    // Typing hides non-matching options (client-side); "man" leaves only Mango.
    await input.fill("man");
    await expect(page.locator('#cb [data-slot="option"][data-value="mango"]')).toBeVisible();
    await expect(page.locator('#cb [data-slot="option"][data-value="apple"]')).toBeHidden();

    expect(errors).toEqual([]);
  });

  test("keyboard: arrows move the active option (aria-activedescendant), Enter selects", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-combobox");
    await page.waitForFunction(() => "Alpine" in window);

    const input = page.locator("#cb-input");
    await input.focus();
    await input.fill("man");

    // ArrowDown activates the first visible option; the input references it.
    await page.keyboard.press("ArrowDown");
    const mango = page.locator('#cb [data-slot="option"][data-value="mango"]');
    await expect(mango).toHaveAttribute("data-active", "true");
    const activeId = await mango.getAttribute("id");
    await expect(input).toHaveAttribute("aria-activedescendant", activeId!);

    // Enter activates the active option: its submit button posts the value.
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/combobox\/pick\?.*fruit=mango/);
    await expect(page.locator('[data-slot="combobox-echo"]')).toContainText("mango");

    expect(errors).toEqual([]);
  });

  test("async: typing swaps the listbox with a server-filtered option list", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-combobox-async");
    await page.waitForFunction(() => "htmx" in window);

    const input = page.locator("#cb-input");
    await input.focus();
    // The full server list is present initially.
    await expect(page.locator('#cb [data-slot="option"]')).toHaveCount(10);

    // Typing triggers the debounced hx-get; the server returns only Cherry.
    await input.fill("cher");
    await expect(page.locator('#cb [data-slot="option"]')).toHaveCount(1);
    await expect(page.locator('#cb [data-slot="option"][data-value="cherry"]')).toBeVisible();

    // A query with no matches swaps in the server-rendered empty state.
    await input.fill("zzz");
    await expect(page.locator('#cb [data-slot="option"]')).toHaveCount(0);
    await expect(page.locator('#cb [data-slot="empty"]')).toBeVisible();

    expect(errors).toEqual([]);
  });

  test("no-JS: server-open listbox + submit-button option posts the value", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-combobox-nojs");

    // StylesOnly: Alpine is absent and the listbox is server-open (readable).
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);
    await expect(page.locator("#cb-listbox")).toBeVisible();

    // Picking an option submits the POST form with the value.
    await page.locator('#cb [data-slot="option"][data-value="apple"]').click();
    await expect(page).toHaveURL(/\/combobox\/pick$/);
    await expect(page.locator('[data-slot="combobox-echo"]')).toContainText("apple");

    expect(errors).toEqual([]);
  });
});

test.describe("Command (P51)", () => {
  test("inline palette: list is always visible, typing filters, arrows loop, Enter runs", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-command");
    await page.waitForFunction(() => "Alpine" in window);

    const input = page.locator("#cmd-input");
    await expect(input).toHaveAttribute("aria-expanded", "true");
    // The grouped list is visible without opening anything.
    await expect(page.locator("#cmd-list")).toBeVisible();
    await expect(page.locator('#cmd [role="group"]')).toHaveCount(2);

    // Filter to the settings navigation item.
    await input.focus();
    await input.fill("set");
    const settings = page.locator('#cmd [data-slot="option"][data-value="settings"]');
    await expect(settings).toBeVisible();
    await expect(page.locator('#cmd [data-slot="option"][data-value="new-file"]')).toBeHidden();

    // ArrowDown activates it; Enter follows the link (a real navigation).
    await page.keyboard.press("ArrowDown");
    await expect(settings).toHaveAttribute("data-active", "true");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/command\/run\?cmd=settings/);
    await expect(page.locator('[data-slot="command-echo"]')).toContainText("settings");

    expect(errors).toEqual([]);
  });

  test("no-JS: the grouped list renders and its links navigate", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-command-nojs");

    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);
    await expect(page.locator("#cmd-list")).toBeVisible();

    await page.locator('#cmd [data-slot="option"][data-value="profile"]').click();
    await expect(page).toHaveURL(/\/command\/run\?cmd=profile/);
    await expect(page.locator('[data-slot="command-echo"]')).toContainText("profile");

    expect(errors).toEqual([]);
  });
});
