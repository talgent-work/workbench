import "@testing-library/jest-dom/vitest";
import { vi } from "vitest";
import { notifyManager } from "@tanstack/react-query";
import { configure } from "@testing-library/react";

// CI tests every workspace in parallel on one runner; under that load the 1s
// default for findBy*/waitFor expired before a multi-step dialog settled.
configure({ asyncUtilTimeout: 3000 });

// API mock resolutions should be observable within an awaited React act block,
// including tests that use fake timers for polling intervals.
notifyManager.setScheduler(queueMicrotask);

Object.defineProperty(window, "scrollTo", {
  value: vi.fn(),
  writable: true,
});

if (!Element.prototype.getAnimations) {
  Object.defineProperty(Element.prototype, "getAnimations", {
    value: () => [],
    writable: true,
  });
}

// jsdom has no layout observer; browser acceptance verifies measured overflow.
if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

// jsdom has no media queries; notifications consult reduced-motion preferences.
if (!window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    value: (query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }),
    writable: true,
  });
}

// jsdom has no viewport; lazily rendered blocks (Mermaid diagrams) see
// themselves as visible at once.
if (!globalThis.IntersectionObserver) {
  globalThis.IntersectionObserver = class IntersectionObserver {
    constructor(private readonly callback: IntersectionObserverCallback) {}
    observe(target: Element) { this.callback([{ isIntersecting: true, target, intersectionRatio: 1 } as IntersectionObserverEntry], this as unknown as globalThis.IntersectionObserver); }
    unobserve() {}
    disconnect() {}
    takeRecords() { return []; }
    readonly root = null; readonly rootMargin = ""; readonly thresholds = [];
  } as unknown as typeof globalThis.IntersectionObserver;
}

// jsdom has no element scrolling; the workspace card scrolls to a folder.
if (!Element.prototype.scrollTo) {
  Object.defineProperty(Element.prototype, "scrollTo", { value: () => {}, writable: true });
}
