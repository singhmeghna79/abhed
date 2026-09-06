#!/bin/bash
# Reads requests and never answers: the hang case.
while IFS= read -r line; do sleep 30; done
