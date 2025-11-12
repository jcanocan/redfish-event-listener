#!/bin/bash

NODE_NAME=$1
CONDITION_TYPE="TestCondition"

if [ -z "$NODE_NAME" ]; then
  echo "Usage: $0 <node-name>" >&2
  exit 1
fi

INDEX=$(kubectl get node "$NODE_NAME" -o json | \
  jq ".status.conditions | map(.type == \"$CONDITION_TYPE\") | index(true)")

if [ "$INDEX" != "null" ]; then
  echo "Condition $CONDITION_TYPE found, removing it"
  PATCH_JSON="[{\"op\": \"remove\", \"path\": \"/status/conditions/$INDEX\"}]"
  kubectl patch node "$NODE_NAME" --subresource=status --type='json' -p "$PATCH_JSON" > /dev/null
fi