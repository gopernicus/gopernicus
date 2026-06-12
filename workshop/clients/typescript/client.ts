// gopernicus:bootstrap kind=tsclient/client.ts template=cb012d6aabfa
// This file is created once by gopernicus and will NOT be overwritten.
// Add app-specific client helpers here — preconfigured constructors,
// wrappers for hand-written (non-generated) routes, retry policies.

import { GopernicusClient, type ClientOptions } from "./client.gen";

/** Construct the app client; extend with environment defaults as needed. */
export function createClient(opts: ClientOptions): GopernicusClient {
  return new GopernicusClient(opts);
}
