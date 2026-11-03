import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { NavigationProvider } from "../../app/navigation";
import i18n from "../../i18n";
import { ArchiveCalendar } from "./ArchiveCalendar";

afterEach(async () => {
  cleanup();
  await i18n.changeLanguage("zh-CN");
});

it.each(["zh-CN", "en"])("uses numeric archive dates in %s", async (locale) => {
  await i18n.changeLanguage(locale);
  render(
    <NavigationProvider>
      <ArchiveCalendar
        months={[{ year: 2026, month: 8, count: 1 }]}
        monthCounts={new Map([["2026-08", 1]])}
        dayCounts={new Map([[10, 1]])}
        visibleMonth="2026-08"
        range={{ oldest: "2026-08", newest: "2026-08" }}
        selectedDay={null}
        today="2026-08-11"
        positioned={false}
        note={null}
        variant="column"
        onVisibleMonthChange={vi.fn()}
      />
    </NavigationProvider>,
  );
  const title = screen.getByRole("button", { name: /2026-08/ });
  expect(title).toHaveTextContent("2026-08");
  expect(screen.getByRole("link", { name: /2026-08-10/ })).toHaveAttribute(
    "href",
    "/archive?day=2026-08-10",
  );
  fireEvent.click(title);
  expect(
    await screen.findByRole("link", { name: /2026-08[，,]/ }),
  ).toHaveTextContent(/^8/);
});
