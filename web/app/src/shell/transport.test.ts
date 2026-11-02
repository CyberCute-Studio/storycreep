import { describe, expect, it } from "vitest";
import { unencrypted } from "./transport";

describe("unencrypted", () => {
  // Verifies: NFR-SEC-15
  it("warns of plain HTTP from any host but the machine's own", () => {
    const at = (url: string) => {
      const { protocol, hostname } = new URL(url);
      return unencrypted({ protocol, hostname });
    };
    for (const url of [
      "http://192.168.1.20:8080/app/",
      "http://10.0.0.5/app/",
      "http://storycreep.lan/app/",
      "http://[fe80::1]:8080/app/",
      "http://127.example.com/app/",
      "http://128.0.0.1/app/",
    ]) {
      expect(at(url), url).toBe(true);
    }
    for (const url of [
      "https://192.168.1.20/app/",
      "https://storycreep.example.com/app/",
      "http://localhost:8080/app/",
      "http://LOCALHOST./app/",
      "http://app.localhost/app/",
      "http://127.0.0.1:8080/app/",
      "http://127.1.2.3/app/",
      "http://[::1]:8080/app/",
    ]) {
      expect(at(url), url).toBe(false);
    }
  });
});
