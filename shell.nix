{ forCI ? false }: let
  pkgs = import <nixpkgs> {};
in
  with pkgs;
  mkShell {
    buildInputs = [
      go
      olm
      pre-commit
    ] ++ lib.lists.optional (!forCI) [
      gotools
      gopls
    ];
  }
