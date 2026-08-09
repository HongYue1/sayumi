import { getErrorMessage } from "~/lib/errors";
import { session } from "~/lib/session";
import { toast } from "~/lib/toast";

/** Signs out locally and surfaces a transport failure from the server request. */
export function signOutWithFeedback(): void {
  void session.logout().catch((error: unknown) => {
    toast.show(
      getErrorMessage(error, "Could not reach the server to sign out"),
    );
  });
}
