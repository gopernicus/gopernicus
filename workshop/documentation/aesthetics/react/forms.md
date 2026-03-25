# Form Patterns

All forms use `@tanstack/react-form` — never `useState` per field.

## Required Pattern

```typescript
function CreateGroupForm({ onSuccess }: { onSuccess?: () => void }) {
  const createGroup = groupHooks.useCreate(tenantId)
  const { status, showError, clearStatus } = useFormStatus()

  const form = useForm({
    defaultValues: { name: "", description: "" },
    onSubmit: async ({ value }) => {
      clearStatus()
      try {
        await createGroup.mutateAsync(value)
        onSuccess?.()
      } catch {
        showError("Failed to create group.")
      }
    },
  })

  return (
    <form onSubmit={(e) => { e.preventDefault(); form.handleSubmit() }}>
      <form.Field name="name">
        {(field) => (
          <FormField label="Name" error={field.state.meta.errors[0]}>
            <Input
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              disabled={createGroup.isPending}
              aria-invalid={!!field.state.meta.errors[0]}
            />
          </FormField>
        )}
      </form.Field>

      <FormStatus status={status} />

      <Button type="submit" disabled={createGroup.isPending}>
        {createGroup.isPending ? "Creating..." : "Create"}
      </Button>
    </form>
  )
}
```

## Submission Flow

1. `clearStatus()` — reset previous feedback
2. Client-side validation via field validators
3. `await mutation.mutateAsync(values)` in `onSubmit`
4. Catch specific errors (`isInvalidCredentials`, `isEmailNotVerified`, `isRateLimited`)
5. `showError(message)` for failures
6. `onSuccess?.()` callback or navigate on success

## useFormStatus Hook

```typescript
import { useFormStatus } from "@<project>/react/ui/components"

const { status, showSuccess, showError, showWarning, showInfo, clearStatus, hasStatus } =
  useFormStatus({ feedback: "inline" })
```

**Feedback modes:**
- `'inline'` — renders `<FormStatus>` component in the form
- `'toast'` — shows toast notification
- `'both'` — inline and toast simultaneously

**Auto-clear:** Optional `autoClearDelay` (ms) to auto-dismiss success messages.

## FormField Component

Wraps a field with label, error display, and description:

```typescript
import { FormField } from "@<project>/react/ui/components"

<form.Field name="email">
  {(field) => (
    <FormField
      label="Email"
      error={field.state.meta.errors[0]}
      description="We'll send a verification email"
      required
    >
      <Input
        value={field.state.value}
        onChange={(e) => field.handleChange(e.target.value)}
        aria-invalid={field.state.meta.isTouched && !!field.state.meta.errors[0]}
        aria-describedby={`email-description`}
      />
    </FormField>
  )}
</form.Field>
```

Automatically wires up `data-invalid`, `data-dirty`, `data-touched` attributes on the control wrapper.

## Mobile Form Pattern

Same `@tanstack/react-form` pattern, different input components:

```typescript
<form.Field name="email" validators={{ onSubmit: validateEmail }}>
  {(field) => (
    <View>
      <Label>Email</Label>
      <Input
        value={field.state.value}
        onChangeText={(text) => field.handleChange(text)}  // onChangeText, not onChange
      />
      {field.state.meta.errors.length > 0 && (
        <Text style={{ color: colors.error }}>
          {field.state.meta.errors[0]}
        </Text>
      )}
    </View>
  )}
</form.Field>
```

## Rules

- **Never use `useState` per field** — always `@tanstack/react-form`
- **Auth forms are reusable components** in `bridge/react/auth/components/` — route files compose them, never inline form logic
- **Disable inputs during submission** via `mutation.isPending`
- **Use `aria-invalid` and `aria-describedby`** for accessibility
