import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DEFAULT_ALLOWED_PATTERNS } from "../lib/refcheck";
import { RegistryForm } from "./RegistryForm";

function renderForm(overrides: Partial<Parameters<typeof RegistryForm>[0]> = {}) {
  const onSubmit = vi.fn();
  const onEdit = vi.fn();
  render(
    <RegistryForm
      allowedRegistries={DEFAULT_ALLOWED_PATTERNS}
      submitting={false}
      error={null}
      onSubmit={onSubmit}
      onEdit={onEdit}
      {...overrides}
    />,
  );
  return { onSubmit, onEdit, input: screen.getByTestId("registry-input") };
}

describe("RegistryForm", () => {
  it("shows nothing and disables the button over an untouched input", () => {
    renderForm();
    expect(screen.queryByTestId("registry-verdict")).toBeNull();
    expect(screen.getByTestId("registry-submit")).toBeDisabled();
  });

  it("reports a parse failure inline and keeps the button disabled (state #7)", async () => {
    const user = userEvent.setup();
    const { input } = renderForm();

    await user.type(input, "not a ref!");
    expect(screen.getByTestId("registry-verdict")).toHaveTextContent("Not a valid image reference");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByTestId("registry-submit")).toBeDisabled();
  });

  it("names the allowed registries when the host is not on the list (state #8)", async () => {
    const user = userEvent.setup();
    const { input } = renderForm();

    await user.type(input, "registry.example.com/x");
    expect(screen.getByTestId("registry-verdict")).toHaveTextContent(
      "registry.example.com isn’t on the allowlist. Allowed: Docker Hub, GHCR, GCR, ECR, ACR.",
    );
    expect(screen.getByTestId("registry-submit")).toBeDisabled();
  });

  it("resolves the registry and enables the button for a valid reference", async () => {
    const user = userEvent.setup();
    const { input, onSubmit } = renderForm();

    await user.type(input, "ghcr.io/org/img:tag");
    expect(screen.getByTestId("registry-verdict")).toHaveTextContent("→ ghcr.io ✓ allowed");
    const submit = screen.getByTestId("registry-submit");
    expect(submit).toBeEnabled();

    await user.click(submit);
    expect(onSubmit).toHaveBeenCalledWith("ghcr.io/org/img:tag");
  });

  it("trims the reference it submits", async () => {
    const user = userEvent.setup();
    const { input, onSubmit } = renderForm();
    await user.type(input, "  alpine:3.20  ");
    await user.click(screen.getByTestId("registry-submit"));
    expect(onSubmit).toHaveBeenCalledWith("alpine:3.20");
  });

  it("shows a server rejection verbatim and tells the caller to clear it on the next keystroke", async () => {
    const user = userEvent.setup();
    const { input, onEdit } = renderForm({
      error: {
        code: "registry_not_allowed",
        message: "evil.example.com is not on the allowlist of registries layerlens may pull from.",
      },
    });
    expect(screen.getByTestId("registry-error")).toHaveTextContent(
      "evil.example.com is not on the allowlist of registries layerlens may pull from.",
    );

    await user.type(input, "g");
    expect(onEdit).toHaveBeenCalled();
  });

  it("uses the allowlist the server reported, not the bundled default", async () => {
    const user = userEvent.setup();
    const { input } = renderForm({ allowedRegistries: ["gcr.io"] });

    await user.type(input, "ghcr.io/org/img");
    expect(screen.getByTestId("registry-verdict")).toHaveTextContent(
      "ghcr.io isn’t on the allowlist. Allowed: GCR.",
    );
  });
});
