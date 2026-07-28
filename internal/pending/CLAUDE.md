# pending

- First Answer/Complete wins; second returns ErrAlreadyResolved or ErrNoWaiter.
- Resolve MUST honor ctx cancellation promptly (stalls the whole turn).
