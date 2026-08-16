import { getErrorMessage } from "~/lib/errors";
import { session } from "~/lib/session";
import { toast } from "~/lib/toast";

/** Signs out locally (teardown runs even when the request fails) and surfaces
 *  any failure of the server request as a toast. */
export function signOutWithFeedback(): void {
  void session.logout().catch((error: unknown) => {
    toast.show(
      getErrorMessage(error, "Could not reach the server to sign out"),
    );
  });
}
