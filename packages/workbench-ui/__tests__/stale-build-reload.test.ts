import { expect, it, vi } from "vitest";
import { installStaleBuildReload } from "../lib/stale-build-reload";

// A page left open across a release asks for chunks the new image no longer
// serves; it reloads once onto the current build, but never loops.
function harness(storage: Storage | (() => never) = memoryStorage()) {
  const events = new EventTarget();
  const reload = vi.fn();
  let clock = 1_000_000;
  const target = {
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    get sessionStorage(): Storage { return typeof storage === "function" ? storage() : storage; },
    location: { reload },
  } as unknown as Parameters<typeof installStaleBuildReload>[0];
  const uninstall = installStaleBuildReload(target, () => clock);
  const fail = () => {
    const event = new Event("vite:preloadError", { cancelable: true });
    events.dispatchEvent(event);
    return event.defaultPrevented;
  };
  return { reload, fail, uninstall, advance: (ms: number) => { clock += ms; } };
}

function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => { values.set(key, value); } } as Storage;
}

it("reloads onto the current build when a chunk of the old one is gone", () => {
  const page = harness();
  expect(page.fail()).toBe(true);
  expect(page.reload).toHaveBeenCalledTimes(1);
});

it("lets a failure right after that reload surface instead of reloading again", () => {
  const storage = memoryStorage();
  harness(storage).fail();
  // The reloaded page shares the tab's session storage.
  const reloaded = harness(storage);
  reloaded.advance(2_000);
  expect(reloaded.fail()).toBe(false);
  expect(reloaded.reload).not.toHaveBeenCalled();
});

it("reloads again for a later release", () => {
  const page = harness();
  page.fail();
  page.advance(60_000);
  expect(page.fail()).toBe(true);
  expect(page.reload).toHaveBeenCalledTimes(2);
});

it("never reloads when it cannot remember having reloaded", () => {
  const page = harness(() => { throw new Error("storage blocked"); });
  expect(page.fail()).toBe(false);
  expect(page.reload).not.toHaveBeenCalled();
});

it("stops listening once uninstalled", () => {
  const page = harness();
  page.uninstall();
  expect(page.fail()).toBe(false);
  expect(page.reload).not.toHaveBeenCalled();
});
