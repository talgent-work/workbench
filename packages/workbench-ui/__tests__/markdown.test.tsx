import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { InlineMarkdown, Markdown } from "../components/markdown";

describe("shared Markdown", () => {
  // Regression (290ac1bb4): long tables were capped in an inner scrolling box.
  it("lets a long table grow in the reading flow instead of scrolling inside a capped box", () => {
    const rows = Array.from({ length: 40 }, (_, i) => `| 行 ${i} | 值 |`).join("\n");
    render(<Markdown>{`| 字段 | 值 |\n| --- | --- |\n${rows}`}</Markdown>);
    const table = screen.getByRole("table");
    for (let element = table.parentElement; element && element !== document.body; element = element.parentElement) {
      expect(element.style.maxHeight).toBe("");
    }
  });

  it("sanitizes unsafe HTML and URLs while retaining safe content", () => {
    const { container } = render(<Markdown>{'<script>alert(1)</script>\n\n<img src="x" onerror="alert(1)" />\n\n[bad](javascript:alert%281%29)\n\n[文档](https://example.test/docs)\n\n<strong>保留内容</strong>'}</Markdown>);
    expect(container.querySelector("script, [onerror], [onclick]")).toBeNull();
    expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
    expect(screen.getByText("保留内容")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "文档" })).toHaveAttribute("rel", "noopener noreferrer");
  });
});

describe("inline Markdown", () => {
  it("sanitizes unsafe HTML and URLs like block Markdown", () => {
    const { container } = render(<InlineMarkdown>{'<img src="x" onerror="alert(1)" /> [bad](javascript:alert%281%29) <script>alert(1)</script>'}</InlineMarkdown>);
    expect(container.querySelector("script, [onerror], [onclick]")).toBeNull();
    expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
  });
});
