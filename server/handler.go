package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/akhenakh/lspgo/jsonrpc2" // Adjust import path
)

// HandlerFunc defines the signature for LSP method handlers.
// It receives context, a connection to reply/notify, and decoded params.
// It returns a result (marshallable to JSON) or an error.
type HandlerFunc func(ctx context.Context, conn *jsonrpc2.Conn, params json.RawMessage) (result interface{}, err error)

// typedHandler wraps a user-provided function with strong parameter typing.
type typedHandler struct {
	h         interface{} // The user's function e.g. func(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error)
	paramType reflect.Type
}

// invoke calls the underlying user handler after decoding params.
func (th *typedHandler) invoke(ctx context.Context, conn *jsonrpc2.Conn, params json.RawMessage) (result interface{}, err error) {
	// Create a new pointer to the parameter type
	paramValuePtr := reflect.New(th.paramType)

	// Decode params if they exist and the target type is not nil
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, paramValuePtr.Interface()); err != nil {
			return nil, jsonrpc2.NewError(jsonrpc2.InvalidParams, fmt.Sprintf("failed to decode params: %v", err))
		}
	} else if th.paramType != nil {
		// If params are required but missing/null, maybe return error? Or let handler decide?
		// Let's allow null/empty if the type is a pointer, otherwise require non-null.
		if th.paramType.Kind() != reflect.Ptr {
			// Check if it's an empty struct - allow that
			isEmptyStruct := th.paramType.Kind() == reflect.Struct && th.paramType.NumField() == 0
			if !isEmptyStruct {
				return nil, jsonrpc2.NewError(jsonrpc2.InvalidParams, "missing non-nullable params")
			}
		}
		// For nil paramType or pointer types, paramValuePtr will hold the zero value (nil pointer)
	}

	// Call the user's handler function using reflection
	hValue := reflect.ValueOf(th.h)
	hType := hValue.Type()

	// Prepare arguments for the call
	args := []reflect.Value{reflect.ValueOf(ctx)}
	// Check if the handler expects the Conn argument
	if hType.NumIn() > 1 && hType.In(1) == reflect.TypeOf((*jsonrpc2.Conn)(nil)) {
		args = append(args, reflect.ValueOf(conn))
		if hType.NumIn() > 2 { // Context, Conn, Params
			args = append(args, paramValuePtr)
		}
	} else if hType.NumIn() > 1 { // Context, Params
		args = append(args, paramValuePtr)
	}

	// Call the handler
	results := hValue.Call(args)

	// Process results
	var resErr error
	if len(results) > 0 {
		// The last return value is conventionally the error
		if errVal, ok := results[len(results)-1].Interface().(error); ok {
			resErr = errVal // Can be nil
		}
	}

	if resErr != nil {
		// Check if it's already a jsonrpc2 error
		if _, ok := resErr.(*jsonrpc2.ErrorObject); ok {
			return nil, resErr // Return as is
		}
		// Wrap other errors as internal server errors
		// TODO: Maybe allow mapping specific Go errors to specific JSON-RPC errors
		return nil, jsonrpc2.NewError(jsonrpc2.InternalError, resErr.Error())
	}

	// If there's a non-error result, return it (first return value)
	if len(results) > 0 && results[0].IsValid() && !results[0].IsNil() {
		// Check if the last value was the error we already processed
		if len(results) > 1 || resErr == nil { // If only one return val, it must be result
			return results[0].Interface(), nil
		}
	}

	// No result, no error
	return nil, nil
}

// Helper to validate user-provided handler function signatures.
// Expected: func(ctx context.Context [, conn *jsonrpc2.Conn], params *protocol.SpecificParams) (result *protocol.SpecificResult, err error)
// Variations allowed: no conn, no params, no result. Error return is optional but recommended.
func validateHandlerFunc(h any) (reflect.Type, error) {
	hType := reflect.TypeOf(h)
	if hType.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function")
	}

	// Check context argument
	if hType.NumIn() < 1 || hType.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		return nil, fmt.Errorf("handler must accept context.Context as first argument")
	}

	argIndex := 1
	// Check optional Conn argument
	if hType.NumIn() > argIndex && hType.In(argIndex) == reflect.TypeOf((*jsonrpc2.Conn)(nil)) {
		argIndex++
	}

	// Check optional Params argument
	var paramType reflect.Type
	if hType.NumIn() > argIndex {
		paramType = hType.In(argIndex)
		// Params should generally be pointers to structs or specific types
		if paramType.Kind() != reflect.Ptr && paramType.Kind() != reflect.Struct && paramType.Kind() != reflect.Interface {
			// Allow basic types too, maybe? For now, stick to structs/pointers/interfaces
			// Also allow nil interface for methods with no params
			isEmptyStruct := paramType.Kind() == reflect.Struct && paramType.NumField() == 0
			if !isEmptyStruct && paramType.Kind() != reflect.Interface {
				// return nil, fmt.Errorf("handler param type %s should be a pointer to a struct or an interface{}", paramType)
				// Relaxing this check for simpler types or empty structs
			}
		}
		// If it's a struct, we actually need the pointer type for unmarshalling
		if paramType.Kind() == reflect.Struct {
			// Except for zero-field structs (like ShutdownParams)
			if paramType.NumField() > 0 {
				// paramType = reflect.PtrTo(paramType) // No, use the struct type directly, reflect.New creates pointer
			}
		}
		argIndex++
	}

	if hType.NumIn() > argIndex {
		return nil, fmt.Errorf("handler has too many input arguments (max context, [conn], [params])")
	}

	// Check return values (optional result, optional error)
	if hType.NumOut() > 2 {
		return nil, fmt.Errorf("handler has too many return values (max result, error)")
	}
	if hType.NumOut() > 0 {
		// Last return must be error if present
		lastReturn := hType.Out(hType.NumOut() - 1)
		if !lastReturn.Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			// If only one return, it must be the result
			if hType.NumOut() == 1 {
				// OK - this is the result
			} else {
				return nil, fmt.Errorf("handler's last return value must be error")
			}
		}
	}
	// Return the detected parameter type (can be nil if no params expected)
	// If paramType is a struct, return the struct type itself, not pointer.
	// reflect.New will create the pointer later.
	if paramType != nil && paramType.Kind() == reflect.Ptr {
		return paramType.Elem(), nil // Use the element type for registration map
	}
	return paramType, nil // Return struct type or nil interface{} type
}
