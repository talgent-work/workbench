import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MessageInput } from "../components/chat/message-input";

describe("MessageInput", () => {
  it("retains a failed asynchronous draft and clears it only after a successful retry", async () => {
    const user = userEvent.setup();
    let rejectSend!: (error: Error) => void;
    const onSend = vi.fn()
      .mockImplementationOnce(() => new Promise<void>((_, reject) => { rejectSend = reject; }))
      .mockResolvedValueOnce(undefined);
    render(<MessageInput disabled={false} onSend={onSend} inputLabel="Instruction" />);
    const input = screen.getByLabelText("Instruction");
    await user.type(input, "  keep this draft  ");
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(input).toHaveValue("  keep this draft  ");
    expect(input).toBeDisabled();

    await act(async () => { rejectSend(new Error("Connection lost")); });
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(input).toHaveValue("  keep this draft  ");
    expect(input).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(input).toHaveValue(""));
    expect(onSend.mock.calls).toEqual([["keep this draft"], ["keep this draft"]]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("admits one send while its result is pending even with repeated Enter and clicks", async () => {
    const user = userEvent.setup();
    let resolveSend!: () => void;
    const onSend = vi.fn(() => new Promise<void>((resolve) => { resolveSend = resolve; }));
    render(<MessageInput disabled={false} onSend={onSend} inputLabel="Instruction" />);
    const input = screen.getByLabelText("Instruction");
    await user.type(input, "send once");
    const send = screen.getByRole("button", { name: "Send" });
    act(() => {
      fireEvent.keyDown(input, { key: "Enter" });
      fireEvent.keyDown(input, { key: "Enter" });
      fireEvent.click(send);
    });
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(send).toBeDisabled();
    expect(input).toHaveValue("send once");
    await act(async () => { resolveSend(); });
    expect(input).toHaveValue("");
    expect(input).toBeEnabled();
  });

  it.each([{ isComposing: true }, { keyCode: 229 }])("does not send Enter during IME composition %j", async (imeEvent) => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<MessageInput disabled={false} onSend={onSend} inputLabel="Instruction" />);
    const input = screen.getByLabelText("Instruction");
    await user.type(input, "待确认");
    fireEvent.keyDown(input, { key: "Enter", ...imeEvent });
    expect(onSend).not.toHaveBeenCalled();
    expect(input).toHaveValue("待确认");
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(input).toHaveValue(""));
    expect(onSend).toHaveBeenCalledWith("待确认");
  });

});
