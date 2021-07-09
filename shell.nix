{ pkgs ? import <nixpkgs> { } }:

pkgs.mkShell {
  buildInputs = with pkgs; [
    go
    goimports
    gopls
    olm
    vgo2nix
  ];
}
