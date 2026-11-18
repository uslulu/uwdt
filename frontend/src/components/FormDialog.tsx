import { useEffect, useRef, type ReactNode } from 'react';

export default function FormDialog({ title, onClose, children }: {
  title: string; onClose: () => void; children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current!;
    const opener = document.activeElement;
    dialog.showModal();
    dialog.querySelector('input')?.focus();
    return () => {
      dialog.close();
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
    };
  }, []);
  return <dialog ref={ref} className="ui-modal sf-dialog" aria-label={title}
    onCancel={event => { event.preventDefault(); onClose(); }}>
    {children}
  </dialog>;
}
