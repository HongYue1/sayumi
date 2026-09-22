/*
 * The house ornament: an aldus leaf drawn as geometry.
 *
 * This used to be the literal "❦" (U+2767) character. None of the bundled
 * faces carry that codepoint and the UI stack has no system fallback to lean
 * on, so on a stock Linux box it rendered as a tofu box. A path renders the
 * same everywhere.
 *
 * Sized in em and filled with currentColor (see .fleuron-svg in app.css), so
 * the font-size and color rules already on each mark keep driving it.
 */
export default function Fleuron(props: { class?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class={props.class ? `fleuron-svg ${props.class}` : "fleuron-svg"}
      viewBox="0 0 32 32"
      aria-hidden="true"
    >
      <path d="M16 24.5C12 18 4 15.5 4 9c0-4.2 3.4-6.5 6.6-4.8C13.2 5.6 15 8.6 16 12c1-3.4 2.8-6.4 5.4-7.8C24.6 2.5 28 4.8 28 9c0 6.5-8 9-12 15.5Z" />
      <path
        d="M16 23.5c1.5 3 1 5.5-1.5 7 2.5-.5 4-2 4.5-4.5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  );
}
