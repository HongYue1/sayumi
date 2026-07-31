// Phase B walking-skeleton stub. Exists only so the shell type-checks and the
// Vite build can populate cmd/sayumi/dist, which unblocks the Go embed gates.
// Replaced by the real port of Read.svelte in Phase C.
interface Props {
  bookId: string;
}

// props is read, never destructured: destructuring would snapshot bookId and
// silently break reactivity, and no linter catches that.
export default function Read(props: Props) {
  return <div class="stub">Read (stub): {props.bookId}</div>;
}
