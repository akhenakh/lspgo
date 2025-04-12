package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/akhenakh/lspgo/jsonrpc2"
)

// HandlerFunc defines the signature for LSP method handlers.
// It receives context, a connection to reply/notify, and decoded params.
// It returns a result (marshallable to JSON) or an error.
// Note: This type is defined but not directly used by the reflection mechanism.
// type HandlerFunc func(ctx context.Context, conn *jsonrpc2.Conn, params json.RawMessage) (result any, err error)

// typedHandler wraps a user-provided function with strong parameter typing.
type typedHandler struct {
	h         any // The user's function e.g. func(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error)
	paramType reflect.Type
	// Add flags to indicate expected arguments like conn
	takesConn   bool
	takesParams bool
}

// invoke calls the underlying user handler after decoding params.
// It now accepts conn *jsonrpc2.Conn and params json.RawMessage.
func (h *typedHandler) invoke(ctx context.Context, conn *jsonrpc2.Conn, params json.RawMessage) (result interface{}, err error) {
	var paramsPtr interface{} // Pointer to the params struct

	if h.takesParams && h.paramType != nil { // Check if parameters are defined and expected for this handler
		paramsValue := reflect.New(h.paramType) // Create a pointer to a new value of the param type
		paramsPtr = paramsValue.Interface()     // Get the pointer as an interface{}

		// Try to unmarshal ONLY if params are present
		if params != nil && len(params) > 0 && string(params) != "null" {
			if err := json.Unmarshal(params, paramsPtr); err != nil {
				// Use specific JSON-RPC error code
				return nil, &jsonrpc2.ErrorObject{
					Code:    jsonrpc2.InvalidParams,
					Message: fmt.Sprintf("failed to unmarshal params: %v", err),
				}
			}
		}
		// If params is null or missing, paramsPtr will point to the zero value struct, which is often fine.
	} else {
		// No parameters defined/expected, or handler signature doesn't take params.
		// Check if the client incorrectly sent parameters when none were expected.
		if params != nil && len(params) > 0 && string(params) != "null" {
			// Decide whether to error or ignore. LSP often ignores extra params in notifications.
			// For requests, returning an error might be better.
			// Let's return an error for now if unexpected params are received by a handler that doesn't expect them.
			if !h.takesParams {
				return nil, &jsonrpc2.ErrorObject{
					Code:    jsonrpc2.InvalidParams,
					Message: "method received unexpected parameters",
				}
			}
			// If h.takesParams is true but h.paramType is nil (e.g. interface{}), we might allow raw params?
			// For now, assume paramType must be non-nil if takesParams is true.
		}
		// paramsPtr remains nil
	}

	// Call the actual handler function using reflection
	handlerFunc := reflect.ValueOf(h.h)
	funcType := handlerFunc.Type()

	// Build arguments for the call
	args := []reflect.Value{reflect.ValueOf(ctx)} // First arg is always context

	argIndex := 1 // Current argument index in the *handler* signature
	if h.takesConn {
		if funcType.NumIn() <= argIndex {
			// This indicates a mismatch between validation and handler signature, should not happen
			return nil, fmt.Errorf("internal error: handler validated to take Conn, but signature mismatch")
		}
		args = append(args, reflect.ValueOf(conn)) // Pass the connection pointer
		argIndex++
	}

	if h.takesParams {
		if funcType.NumIn() <= argIndex {
			// This indicates a mismatch between validation and handler signature, should not happen
			return nil, fmt.Errorf("internal error: handler validated to take Params, but signature mismatch")
		}
		paramArgType := funcType.In(argIndex)
		if paramsPtr != nil {
			// Pass the unmarshalled params (or zero value pointer)
			paramValue := reflect.ValueOf(paramsPtr) // This is reflect.Value of the *pointer*
			// If handler expects a value type, dereference pointer.
			// Careful: Check Kind() before Elem()
			if paramArgType.Kind() != reflect.Ptr && paramValue.IsValid() && !paramValue.IsNil() {
				args = append(args, paramValue.Elem())
			} else {
				// Pass pointer directly (or nil pointer if unmarshalling failed/no params)
				args = append(args, paramValue)
			}
		} else {
			// Pass nil or zero value if handler expects params but none were defined/sent
			// Note: paramsPtr is nil here. We need the *type* expected by the handler.
			args = append(args, reflect.Zero(paramArgType)) // Pass zero value for the expected type
		}
		argIndex++
	}

	// Check if the number of arguments matches
	if funcType.NumIn() != len(args) {
		return nil, fmt.Errorf("internal error: argument count mismatch calling handler. Expected %d, got %d", funcType.NumIn(), len(args))
	}

	// Call the handler
	results := handlerFunc.Call(args)

	// Process results (assuming handler returns (result, error) or just error or just result)
	var resErr error
	var resVal interface{} // Use specific variable for result

	if len(results) == 1 {
		// Single return value: could be result or error
		if errVal, ok := results[0].Interface().(error); ok {
			resErr = errVal // It's an error
		} else {
			resVal = results[0].Interface() // It's a result
		}
	} else if len(results) == 2 {
		// Two return values: assume (result, error)
		if !results[0].IsNil() {
			resVal = results[0].Interface()
		}
		if !results[1].IsNil() {
			if errVal, ok := results[1].Interface().(error); ok {
				resErr = errVal
			} else {
				// Should not happen if signature validation is correct
				return nil, fmt.Errorf("internal error: handler returned non-error as second value")
			}
		}
	}
	// If len(results) == 0, resVal is nil, resErr is nil

	return resVal, resErr // Return result and error from the handler call
}

// Helper to validate user-provided handler function signatures.
// Expected: func(ctx context.Context [, conn *jsonrpc2.Conn], params *protocol.SpecificParams) (result *protocol.SpecificResult, err error)
// Variations allowed: no conn, no params, no result. Error return is optional but recommended.
// Returns paramType, takesConn, takesParams, error
func validateHandlerFunc(h any) (paramType reflect.Type, takesConn bool, takesParams bool, err error) {
	hType := reflect.TypeOf(h)
	if hType.Kind() != reflect.Func {
		err = fmt.Errorf("handler must be a function")
		return
	}

	// Check context argument
	if hType.NumIn() < 1 || hType.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		err = fmt.Errorf("handler must accept context.Context as first argument")
		return
	}

	expectedArgIndex := 1
	// Check optional Conn argument
	if hType.NumIn() > expectedArgIndex && hType.In(expectedArgIndex) == reflect.TypeOf((*jsonrpc2.Conn)(nil)) {
		takesConn = true
		expectedArgIndex++
	}

	// Check optional Params argument
	if hType.NumIn() > expectedArgIndex {
		paramType = hType.In(expectedArgIndex)
		// Params should generally be pointers to structs or specific types that are JSON-unmarshallable
		// Allow structs directly (passed by value to handler), pointers, interfaces, or basic types.
		// We need the concrete type (even if it's a pointer type) for unmarshalling.
		// If it's a pointer, store the Elem type for reflect.New, otherwise store the type itself.
		if paramType.Kind() == reflect.Ptr {
			// Store the element type because reflect.New needs the base type
			paramType = paramType.Elem()
		} else if paramType.Kind() == reflect.Struct || paramType.Kind() == reflect.Interface || paramType.Kind() == reflect.Map || paramType.Kind() == reflect.Slice || paramType.Kind() == reflect.String || paramType.Kind() == reflect.Bool || paramType.Kind() == reflect.Int || paramType.Kind() == reflect.Uint || paramType.Kind() == reflect.Float32 || paramType.Kind() == reflect.Float64 {
			// Keep the type as is
		} else {
			err = fmt.Errorf("handler param type %s must be a pointer to a struct, a struct, interface, map, slice, or basic type", paramType)
			return
		}
		takesParams = true
		expectedArgIndex++
	}

	if hType.NumIn() > expectedArgIndex {
		err = fmt.Errorf("handler has too many input arguments (max context, [conn], [params])")
		return
	}

	// Check return values (optional result, optional error)
	if hType.NumOut() > 2 {
		err = fmt.Errorf("handler has too many return values (max result, error)")
		return
	}
	errorInterface := reflect.TypeOf((*error)(nil)).Elem()
	if hType.NumOut() > 0 {
		// Last return must be error if present and > 0 returns
		lastReturn := hType.Out(hType.NumOut() - 1)
		if !lastReturn.Implements(errorInterface) {
			// If only one return, it must be the result (not error)
			if hType.NumOut() == 1 {
				// OK - this is the result
			} else {
				// If two returns, the last *must* be error
				err = fmt.Errorf("handler's last return value must be error if multiple values are returned")
				return
			}
		}
	}
	// If hType.NumOut() == 2, first is result, second is error (validated above)
	// If hType.NumOut() == 1, it could be result OR error (validated above)
	// If hType.NumOut() == 0, that's fine.

	// Return the detected parameter type (can be nil if no params expected)
	// paramType is already the base type (if it was originally a pointer) or the type itself.
	return // Use named return values
}
